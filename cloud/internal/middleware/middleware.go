// Package middleware contains the shared HTTP middleware chain used by
// the api and relay roles: recover, requestid, logging, metrics, cors.
//
// The order in which these are mounted matters (recover must be
// outermost so it catches panics from logging/metrics; cors must run
// after logging so preflight requests still appear in the access log).
// Callers wire them via server.Router; we expose each individually so
// tests can pin behaviour in isolation.
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyUserID
	ctxKeyServerID
)

// RequestID middleware mints a UUIDv7 if the inbound X-Request-Id
// header is absent or malformed; otherwise it propagates the caller's
// value. UUIDv7 gives us monotonic ordering which is convenient for
// log indexing.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !isUUIDv7(id) {
			u, err := uuid.NewV7()
			if err != nil {
				u = uuid.New()
			}
			id = u.String()
		}
		ctx := context.WithValue(r.Context(), ctxKeyRequestID, id)
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func isUUIDv7(s string) bool {
	u, err := uuid.Parse(s)
	if err != nil {
		return false
	}
	return u.Version() == 7
}

// GetRequestID returns the mint/propagated request id, or "" if the
// caller skipped the middleware.
func GetRequestID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRequestID).(string)
	return v
}

// WithUserID stashes the authenticated user id on ctx so downstream
// handlers and the access log can read it without re-parsing the JWT.
func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyUserID, id)
}

func GetUserID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyUserID).(string)
	return v
}

// WithServerID is the equivalent for the relay tunnel: each tunneled
// request carries the originating server's id for billing + abuse
// tracking.
func WithServerID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyServerID, id)
}

func GetServerID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyServerID).(string)
	return v
}

// Recover converts panics into 500 responses and logs the stack at
// error level. Without this the http.Server prints the stack to stderr
// and silently closes the connection, which is hostile to observability.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.ErrorContext(r.Context(), "panic",
						"request_id", GetRequestID(r.Context()),
						"path", r.URL.Path,
						"panic", rec,
						"stack", string(debug.Stack()),
					)
					http.Error(w, `{"error":"internal_error"}`, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder wraps the ResponseWriter so the logging middleware can
// observe the status code chosen by the handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Logging emits a structured access log entry per request. Fields match
// the bootstrap plan §4: request_id, method, path, status, bytes,
// latency_ms, remote_ip.
func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			logger.LogAttrs(r.Context(), slog.LevelInfo, "http",
				slog.String("request_id", GetRequestID(r.Context())),
				slog.String("user_id", GetUserID(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Int64("latency_ms", time.Since(start).Milliseconds()),
				slog.String("remote_ip", clientIP(r)),
			)
		})
	}
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	return r.RemoteAddr
}

// CORS allows the public Maktaba origins for browser-facing endpoints.
// We intentionally do not echo arbitrary Origin headers — the allow
// list is short and well-known.
func CORS(allowed []string) func(http.Handler) http.Handler {
	allow := map[string]bool{}
	for _, o := range allowed {
		allow[o] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allow[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
