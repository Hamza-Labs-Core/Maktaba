package httpx

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// ProblemHeader names the problem a problem+json answer carries (Write sets
// it): the request log writes it next to the status.
const ProblemHeader = "Maktaba-Problem"

// notYet are the problems of a transcode's file not written yet: a player
// asks for its playlist before the first segment lands, every cold start.
// They are logged at Info, not Warn (the request's line is still there).
var notYet = map[string]bool{
	"manifest-not-found":  true,
	"rendition-not-found": true,
	"segment-not-found":   true,
}

// LogFailures logs every /stream/* request (the bytes a player fetches: VOD,
// posters, live channels) answered with a status of 400 or more as
// `stream_request_failed` (Warn; Error from 500; Info for a file a running
// transcode has not written yet): method, path, status, the
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
			// the cluster's internal calls (a 404 is an answer there) are not a player's
			if !strings.HasPrefix(r.URL.Path, "/stream/") {
				next.ServeHTTP(w, r)
				return
			}
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			t0 := time.Now()
			next.ServeHTTP(ww, r)
			status := ww.Status()
			if status < http.StatusBadRequest {
				return
			}
			problem := ww.Header().Get(ProblemHeader)
			level := slog.LevelWarn
			switch {
			case status >= http.StatusInternalServerError:
				level = slog.LevelError
			case status == http.StatusNotFound && notYet[problem]:
				level = slog.LevelInfo
			}
			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"problem", problem,
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
