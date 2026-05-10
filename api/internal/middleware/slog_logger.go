package middleware

import (
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/reqid"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// statusCapture wraps an http.ResponseWriter so SLogLogger can capture
// the status code emitted by the handler. The shape mirrors
// guardedWriter but is intentionally separate — chaining the two would
// require interface negotiation for hijack/flush that isn't worth the
// complexity for the skeleton.
type statusCapture struct {
	http.ResponseWriter
	status int
}

func (s *statusCapture) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusCapture) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

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
		next.ServeHTTP(sw, r)
		dur := time.Since(t0)

		mlog.From(ctx).Info("http_request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", dur.Milliseconds(),
			"event", "http_request",
		)
	})
}
