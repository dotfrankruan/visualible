package server

import (
	"net/http"
	"time"

	"github.com/dotfrankruan/visualible/internal/logging"
)

// statusRecorder captures the response status and size for access logs.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Flush keeps SSE streaming working through the recorder.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// withRequestLogging records every API call. Successful requests are
// DEBUG (verbose mode); client errors are WARN and server errors ERROR,
// so the default console stays quiet but problems are always visible.
func withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Static assets are only interesting in verbose mode.
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		duration := time.Since(start)
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration", duration.Round(time.Microsecond),
		}
		if q := r.URL.RawQuery; q != "" {
			attrs = append(attrs, "query", q)
		}
		switch {
		case rec.status >= 500:
			logging.Error("http request", attrs...)
		case rec.status >= 400:
			logging.Warn("http request", attrs...)
		default:
			logging.Debug("http request", attrs...)
		}
	})
}
