package middleware

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// DefaultInternalRatePerMin is the home's own callers' budget (InternalCallers): sized for
// a busy home, where each plugin job mints and ends a token on this API (the pipeline's
// plugins/dispatch.py) and a 20,000-video library queues thousands of jobs at once.
const DefaultInternalRatePerMin = 60000

// internalCallerKey marks a request InternalCallers proved this machine's own.
type internalCallerKey struct{}

// InternalCallers gives the home's own processes their own rate budget: a request carrying
// the supervisor's per-boot local token (FromSupervisor: the pipeline, the launcher's
// version and health checks, the supervisor's update watch) draws on one bucket of perMin
// a minute (0 or less: unlimited), and the client limits (PerIP, PerUser) let it by.
//
// The token is the identity, never the address: a reverse proxy and the relay agent also
// reach the API over loopback, and their clients keep the client limits. A request with a
// wrong, partial or empty token, or with none configured, is a client's and passes on
// unmarked. Plugin runners never hold the token (the supervisor drops it from their
// environment). It attaches no principal: each route's own check still decides who the
// caller is, so roles are unchanged. It must run before PerIP and PerUser; the sign-in
// routes' table (AuthRouteRateLimit) does not read the mark.
func InternalCallers(perMin int) func(http.Handler) http.Handler {
	var s *rlStore
	if perMin > 0 {
		s = newRLStore(perMin)
		go runSweep(s)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !FromSupervisor(r) {
				next.ServeHTTP(w, r)
				return
			}
			if s != nil {
				if ok, retry := s.take("internal"); !ok {
					w.Header().Set("Retry-After", strconv.Itoa(retry))
					httperror.Write(w, r, &httperror.Error{
						Type:   httperror.TypeRateLimited,
						Title:  "too many requests",
						Status: http.StatusTooManyRequests,
						Detail: "internal callers' rate limit exceeded",
					})
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), internalCallerKey{}, true)))
		})
	}
}

// InternalCaller reports whether InternalCallers proved the request this machine's own
// (false where it did not run).
func InternalCaller(r *http.Request) bool {
	on, _ := r.Context().Value(internalCallerKey{}).(bool)
	return on
}
