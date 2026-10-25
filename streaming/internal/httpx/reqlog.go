package httpx

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// ProblemHeader names the problem a problem+json answer carries (Write sets
// it): the request log writes it next to the status.
const ProblemHeader = "Maktaba-Problem"

// LogFailures logs every request answered with a status of 400 or more as
// `stream_request_failed` (Warn; Error from 500): method, path, status, the
// problem's name, bytes written and duration. A successful request writes
// nothing (a player fetches a segment every few seconds).
//
// The query is never logged: on these routes it is the signature (?sig=, the
// front door's fx, a preview's pt).
func LogFailures(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			t0 := time.Now()
			next.ServeHTTP(ww, r)
			status := ww.Status()
			if status < http.StatusBadRequest {
				return
			}
			level := slog.LevelWarn
			if status >= http.StatusInternalServerError {
				level = slog.LevelError
			}
			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"problem", ww.Header().Get(ProblemHeader),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(t0).Milliseconds(),
			}
			if id := middleware.GetReqID(r.Context()); id != "" {
				attrs = append(attrs, "request_id", id)
			}
			attrs = append(attrs, "event", "stream_request_failed")
			logger.Log(r.Context(), level, "stream request failed", attrs...)
		})
	}
}
