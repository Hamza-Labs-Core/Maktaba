package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/reqid"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// statusCapture wraps an http.ResponseWriter so SLogLogger can capture
// the status code emitted by the handler. Only WriteHeader is
// overridden; an implicit-200 (handler wrote bytes without calling
// WriteHeader) leaves status==0 and is normalised to 200 at log-emit.
// Deliberately no Write override — a transparent byte passthrough
// adds no value and creates a CodeQL go/reflected-xss false-positive
// sink for every handler whose body carries request-derived data.
type statusCapture struct {
	http.ResponseWriter
	status int
}

func (s *statusCapture) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush passes through to the wrapped writer: streaming handlers (the
// SSE channels under /ws/*) need an http.Flusher, and this wrapper sits
// on every request.
func (s *statusCapture) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusCapture) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// SLogLogger emits one structured log line per request. Story 7.1 AC-2
// requires every line during the request to carry request_id; this
// middleware seeds the value on the context via shared/log/go so the
// downstream logger.From(ctx) calls inherit it.
func SLogLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := reqid.FromContext(r.Context()).String()
		ctx := mlog.WithRequestID(r.Context(), id)
		r = r.WithContext(ctx)

		sw := &statusCapture{ResponseWriter: w}
		t0 := time.Now()
		// A panic passes through to the Recoverer (above this one), which
		// answers 500: the request is still logged, as that.
		defer func() {
			if v := recover(); v != nil {
				logRequest(r, http.StatusInternalServerError, time.Since(t0))
				panic(v)
			}
		}()
		next.ServeHTTP(sw, r)

		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		logRequest(r, status, time.Since(t0))
	})
}

// logRequest writes the request's line: Info, Warn for a 5xx (whose reason
// httperror.Write logs as `http_error`).
func logRequest(r *http.Request, status int, dur time.Duration) {
	level := slog.LevelInfo
	if status >= http.StatusInternalServerError {
		level = slog.LevelWarn
	}
	mlog.From(r.Context()).Log(r.Context(), level, "http_request",
		"method", r.Method,
		"path", r.URL.Path,
		"status", status,
		"duration_ms", dur.Milliseconds(),
		"event", "http_request",
	)
}
