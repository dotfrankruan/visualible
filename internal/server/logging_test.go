package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dotfrankruan/visualible/internal/logging"
)

// captureLogs redirects package logging into a buffer for the duration of
// a test.
func captureLogs(t *testing.T, level string) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	if err := logging.Setup(logging.Config{Level: level, Format: "text", Output: buf, TimeFormat: "15:04:05.000"}); err != nil {
		t.Fatalf("setup logging: %v", err)
	}
	t.Cleanup(func() { _ = logging.Setup(logging.Config{Level: "info"}) })
	return buf
}

func TestRequestLoggingLevels(t *testing.T) {
	buf := captureLogs(t, "debug")

	handler := withRequestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
		case "/bad":
			w.WriteHeader(http.StatusBadRequest)
		case "/boom":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))

	for _, path := range []string{"/ok", "/bad", "/boom"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	}

	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 log lines, got %d:\n%s", len(lines), out)
	}
	// Successful calls are DEBUG; client errors WARN; server errors ERROR.
	wantRank := []string{"DEBUG", "WARN", "ERROR"}
	for i, rank := range wantRank {
		if !strings.Contains(lines[i], " "+rank+" ") {
			t.Errorf("line %d = %q, want rank %s", i, lines[i], rank)
		}
	}
	// Every line carries method/path/status/duration for filtering.
	for _, want := range []string{"method=GET", "path=/ok", "status=200", "duration="} {
		if !strings.Contains(out, want) {
			t.Errorf("access log missing %q:\n%s", want, out)
		}
	}
}

func TestRequestLoggingQuietByDefault(t *testing.T) {
	buf := captureLogs(t, "info")
	handler := withRequestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if strings.Contains(buf.String(), "http request") {
		t.Errorf("successful requests should not be logged at info level: %q", buf.String())
	}

	// Problems are still reported without verbose mode.
	bad := withRequestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	rec = httptest.NewRecorder()
	bad.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/render", nil))
	if !strings.Contains(buf.String(), "ERROR") {
		t.Errorf("server errors must be logged at info level: %q", buf.String())
	}
}
