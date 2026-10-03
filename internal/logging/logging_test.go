package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func capture(t *testing.T, cfg Config) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	cfg.Output = buf
	if cfg.TimeFormat == "" {
		cfg.TimeFormat = "15:04:05.000"
	}
	if err := Setup(cfg); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Cleanup(func() { _ = Setup(Config{Level: "info"}) })
	return buf
}

func TestParseLevel(t *testing.T) {
	valid := map[string]slog.Level{
		"":         LevelInfo,
		"info":     LevelInfo,
		"INFO":     LevelInfo,
		"debug":    LevelDebug,
		"verbose":  LevelDebug,
		"warn":     LevelWarn,
		"warning":  LevelWarn,
		"error":    LevelError,
		"fatal":    LevelFatal,
		"critical": LevelFatal,
		" Debug  ": LevelDebug,
	}
	for in, want := range valid {
		got, err := ParseLevel(in)
		if err != nil {
			t.Errorf("ParseLevel(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Fatal("expected error for unknown level")
	}
}

func TestLevelNames(t *testing.T) {
	cases := map[slog.Level]string{
		LevelDebug: "DEBUG",
		LevelInfo:  "INFO",
		LevelWarn:  "WARN",
		LevelError: "ERROR",
		LevelFatal: "FATAL",
	}
	for lvl, want := range cases {
		if got := LevelName(lvl); got != want {
			t.Errorf("LevelName(%v) = %q, want %q", lvl, got, want)
		}
	}
}

func TestTextFormatAndRanking(t *testing.T) {
	buf := capture(t, Config{Level: "debug"})
	Info("ansible detected", "version", "2.21.4", "modules", 8862)
	Debug("http request", "method", "GET", "path", "/api/health", "status", 200)
	Warn("module normalization", "module", "foo", "err", "unknown option type")
	Error("deployment failed", "deployment", "dep-1", "exit", 2)

	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d:\n%s", len(lines), out)
	}
	// Every line starts with a timestamp then a fixed-width rank.
	for i, want := range []string{"INFO", "DEBUG", "WARN", "ERROR"} {
		if !strings.Contains(lines[i], " "+want) {
			t.Errorf("line %d missing rank %s: %q", i, want, lines[i])
		}
	}
	if !strings.Contains(lines[0], "ansible detected") ||
		!strings.Contains(lines[0], "version=2.21.4") ||
		!strings.Contains(lines[0], "modules=8862") {
		t.Errorf("info line wrong: %q", lines[0])
	}
	// Values with spaces are quoted so columns stay parseable.
	if !strings.Contains(lines[2], `err="unknown option type"`) {
		t.Errorf("spaced value not quoted: %q", lines[2])
	}
}

func TestVerboseGating(t *testing.T) {
	buf := capture(t, Config{Level: "info"})
	Debug("should not appear")
	Info("should appear")
	out := buf.String()
	if strings.Contains(out, "should not appear") {
		t.Errorf("debug leaked at info level: %q", out)
	}
	if !strings.Contains(out, "should appear") {
		t.Errorf("info missing: %q", out)
	}
	if Enabled(LevelDebug) {
		t.Error("debug should be disabled at info level")
	}
	if !Enabled(LevelInfo) || !Enabled(LevelError) {
		t.Error("info/error should be enabled at info level")
	}
}

func TestSensitiveValuesRedacted(t *testing.T) {
	buf := capture(t, Config{Level: "debug"})
	Info("credential stored",
		"credential", "cred-1",
		"secret", "-----BEGIN PRIVATE KEY-----",
		"password", "hunter2",
		"api_key", "sk-live-123",
		"token", "abc",
		"name", "My SSH key")

	out := buf.String()
	for _, leak := range []string{"BEGIN PRIVATE KEY", "hunter2", "sk-live-123", "abc"} {
		if strings.Contains(out, leak) {
			t.Errorf("secret %q leaked into logs: %q", leak, out)
		}
	}
	if strings.Count(out, Redacted) != 4 {
		t.Errorf("expected 4 redactions, got %d: %q", strings.Count(out, Redacted), out)
	}
	// Non-sensitive values still appear.
	if !strings.Contains(out, "name=\"My SSH key\"") {
		t.Errorf("non-sensitive value lost: %q", out)
	}
}

func TestValueHelper(t *testing.T) {
	if got := Value("api_key", "sk-1"); got != Redacted {
		t.Errorf("Value(api_key) = %v", got)
	}
	if got := Value("path", "/tmp/x"); got != "/tmp/x" {
		t.Errorf("Value(path) = %v", got)
	}
	// "key" alone is not treated as sensitive (cache keys, hash keys).
	if got := Value("cache_key", "abc"); got != "abc" {
		t.Errorf("cache_key should not be redacted, got %v", got)
	}
}

func TestJSONFormat(t *testing.T) {
	buf := capture(t, Config{Level: "info", Format: "json"})
	Info("deployment started", "deployment", "dep-1", "hosts", 3, "secret", "shh")
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("json log not parseable: %v (%q)", err, buf.String())
	}
	if entry["level"] != "INFO" {
		t.Errorf("level = %v", entry["level"])
	}
	if entry["msg"] != "deployment started" {
		t.Errorf("msg = %v", entry["msg"])
	}
	if entry["deployment"] != "dep-1" {
		t.Errorf("deployment = %v", entry["deployment"])
	}
	if entry["secret"] != Redacted {
		t.Errorf("secret not redacted in json: %v", entry["secret"])
	}
}

func TestSetupRejectsBadConfig(t *testing.T) {
	if err := Setup(Config{Level: "nope"}); err == nil {
		t.Error("expected error for bad level")
	}
	if err := Setup(Config{Level: "info", Format: "xml"}); err == nil {
		t.Error("expected error for bad format")
	}
}

func TestFatalExits(t *testing.T) {
	buf := capture(t, Config{Level: "info"})
	orig := exitFunc
	called := 0
	exitFunc = func(code int) { called = code }
	defer func() { exitFunc = orig }()

	Fatal("data directory unusable", "path", "/nope", "err", "permission denied")

	if called != 1 {
		t.Fatalf("expected exit code 1, got %d", called)
	}
	out := buf.String()
	if !strings.Contains(out, "FATAL") || !strings.Contains(out, "data directory unusable") {
		t.Fatalf("fatal not logged: %q", out)
	}
}
