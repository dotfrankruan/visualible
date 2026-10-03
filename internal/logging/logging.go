// Package logging provides Visualible's leveled logger.
//
// Every line carries a standard severity rank so the console output can be
// filtered and parsed by ordinary tooling:
//
//	2026-10-03T19:42:11.123+02:00 INFO  ansible detected            version=2.21.4 modules=8862
//	2026-10-03T19:42:11.201+02:00 DEBUG http request                method=GET path=/api/actions status=200 duration=1.2ms
//	2026-10-03T19:42:12.004+02:00 WARN  module normalization        module=foo err="unknown option type"
//
// DEBUG is off unless verbose mode is enabled (-verbose, -log-level=debug
// or VISUALIBLE_LOG_LEVEL=debug). Values under sensitive keys are redacted
// before they are written; secrets must never reach the console.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// Ranks match the conventional syslog-derived ordering used by most
// infrastructure tools.
const (
	LevelDebug = slog.LevelDebug // -4
	LevelInfo  = slog.LevelInfo  //  0
	LevelWarn  = slog.LevelWarn  //  4
	LevelError = slog.LevelError //  8
	// LevelFatal is a Visualible extension above ERROR: it is always
	// emitted (subject to the configured level) and terminates the process
	// with exit code 1.
	LevelFatal = slog.Level(12)
)

// Redacted replaces any value logged under a sensitive key.
const Redacted = "«redacted»"

// Format selects the output encoding.
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// Config configures the process logger.
type Config struct {
	// Level is one of debug, info, warn, error (case-insensitive).
	Level string
	// Format is "text" (default) or "json".
	Format string
	// Output defaults to stderr.
	Output io.Writer
	// TimeFormat overrides the text timestamp layout (tests).
	TimeFormat string
}

var (
	mu       sync.RWMutex
	level    = LevelInfo
	logger   = slog.New(newTextHandler(os.Stderr, LevelInfo, time.RFC3339Nano))
	exitFunc = os.Exit
)

// Setup installs the process logger and returns an error for invalid
// configuration (so a bad flag fails loudly instead of silently
// downgrading logging).
func Setup(cfg Config) error {
	lvl, err := ParseLevel(cfg.Level)
	if err != nil {
		return err
	}
	out := cfg.Output
	if out == nil {
		out = os.Stderr
	}
	format := Format(strings.ToLower(strings.TrimSpace(cfg.Format)))
	if format == "" {
		format = FormatText
	}

	var handler slog.Handler
	switch format {
	case FormatText:
		handler = newTextHandler(out, lvl, cfg.TimeFormat)
	case FormatJSON:
		handler = slog.NewJSONHandler(out, &slog.HandlerOptions{Level: lvl})
	default:
		return fmt.Errorf("unknown log format %q (want text or json)", cfg.Format)
	}
	// Redaction wraps whichever encoder is used, so no format can leak a
	// secret value.
	handler = redactingHandler{inner: handler}

	mu.Lock()
	level = lvl
	logger = slog.New(handler)
	mu.Unlock()
	return nil
}

// ParseLevel accepts the standard rank names plus a few aliases.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return LevelInfo, nil
	case "debug", "verbose", "trace":
		return LevelDebug, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error", "err":
		return LevelError, nil
	case "fatal", "critical", "crit":
		return LevelFatal, nil
	default:
		return 0, fmt.Errorf("unknown log level %q (want debug, info, warn, error or fatal)", s)
	}
}

// LevelName renders a level as its standard rank label.
func LevelName(l slog.Level) string {
	switch {
	case l >= LevelFatal:
		return "FATAL"
	case l >= LevelError:
		return "ERROR"
	case l >= LevelWarn:
		return "WARN"
	case l >= LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}

// Current returns the configured minimum level.
func Current() slog.Level {
	mu.RLock()
	defer mu.RUnlock()
	return level
}

// Enabled reports whether a level is currently emitted.
func Enabled(l slog.Level) bool { return l >= Current() }

// Debug logs at DEBUG (verbose mode only).
func Debug(msg string, args ...any) { log(LevelDebug, msg, args...) }

// Info logs at INFO.
func Info(msg string, args ...any) { log(LevelInfo, msg, args...) }

// Warn logs at WARN.
func Warn(msg string, args ...any) { log(LevelWarn, msg, args...) }

// Error logs at ERROR.
func Error(msg string, args ...any) { log(LevelError, msg, args...) }

// Fatal logs at FATAL and exits with status 1. Use only for conditions
// the process cannot continue from (unusable data directory, port in use).
func Fatal(msg string, args ...any) {
	log(LevelFatal, msg, args...)
	exitFunc(1)
}

func log(l slog.Level, msg string, args ...any) {
	if !Enabled(l) {
		return
	}
	mu.RLock()
	lg := logger
	mu.RUnlock()
	lg.Log(context.Background(), l, msg, args...)
}

// --- sensitive value handling ---------------------------------------

// sensitiveKeys are substrings that mark a key as carrying secret
// material. Values under these keys are replaced with Redacted.
var sensitiveKeys = []string{
	"secret", "password", "passwd", "token", "apikey", "api_key",
	"private_key", "privatekey", "credential_value", "authorization",
	"bearer", "passphrase",
}

// IsSensitiveKey reports whether a key name carries secret material.
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// Value redacts a value when its key is sensitive; otherwise it passes
// the value through unchanged. Call it at any log site that could carry
// user credentials.
func Value(key string, value any) any {
	if IsSensitiveKey(key) {
		return Redacted
	}
	return value
}

// redactingHandler replaces values logged under sensitive keys before
// they reach any encoder (text or JSON).
type redactingHandler struct {
	inner slog.Handler
}

func (h redactingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		nr.AddAttrs(redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, nr)
}

func (h redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, redactAttr(a))
	}
	return redactingHandler{inner: h.inner.WithAttrs(out)}
}

func (h redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{inner: h.inner.WithGroup(name)}
}

// redactAttr redacts a sensitive value; groups are redacted recursively.
func redactAttr(a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		out := make([]slog.Attr, 0, len(group))
		for _, g := range group {
			out = append(out, redactAttr(g))
		}
		a.Value = slog.GroupValue(out...)
		return a
	}
	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	return a
}

// --- text handler ---------------------------------------------------

// textHandler renders lines as:
//
//	<timestamp> LEVEL  <message>  key=value key=value
//
// Message and values are padded/quoted so the output stays scannable.
type textHandler struct {
	mu         *sync.Mutex // shared between derived handlers
	out        io.Writer
	level      slog.Level
	timeFormat string
	attrs      []slog.Attr
	groups     []string
}

func newTextHandler(out io.Writer, level slog.Level, timeFormat string) *textHandler {
	if timeFormat == "" {
		timeFormat = "2006-01-02T15:04:05.000Z07:00"
	}
	return &textHandler{mu: &sync.Mutex{}, out: out, level: level, timeFormat: timeFormat}
}

func (h *textHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *textHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Time.Format(h.timeFormat))
	b.WriteByte(' ')
	b.WriteString(fmt.Sprintf("%-5s", LevelName(r.Level)))
	b.WriteByte(' ')
	b.WriteString(r.Message)

	writeAttr := func(a slog.Attr) {
		if a.Key == "" {
			return
		}
		val := h.formatValue(a)
		b.WriteByte(' ')
		b.WriteString(a.Key)
		b.WriteByte('=')
		b.WriteString(val)
	}

	for _, a := range h.attrs {
		writeAttr(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(a)
		return true
	})

	b.WriteByte('\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.out, b.String())
	return err
}

func (h *textHandler) formatValue(a slog.Attr) string {
	v := a.Value.Resolve()
	if IsSensitiveKey(a.Key) {
		return Redacted
	}
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		if s == "" {
			return `""`
		}
		if strings.ContainsAny(s, " \t\"=") {
			return fmt.Sprintf("%q", s)
		}
		return s
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().Format(time.RFC3339)
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			return fmt.Sprintf("%q", err.Error())
		}
		s := fmt.Sprint(v.Any())
		if strings.ContainsAny(s, " \t\"=") {
			return fmt.Sprintf("%q", s)
		}
		return s
	default:
		return v.String()
	}
}

func (h *textHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cp := *h
	cp.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &cp
}

func (h *textHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	cp := *h
	cp.groups = append(append([]string{}, h.groups...), name)
	return &cp
}
