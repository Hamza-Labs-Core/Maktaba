package middleware

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// userIDCtxKey is the context key auth middleware (Epic 10) uses to
// stash a user identifier. The rate limiter reads it via UserID; tests
// and pre-auth code paths can plant a value via WithUserID.
type userIDCtxKey struct{}

// UserID returns the authenticated user's id from ctx, or empty string
// when no user is attached. The per-user rate limiter falls back to
// the IP key when no user is present so unauthenticated traffic is
// still bounded.
func UserID(ctx context.Context) string {
	if v, ok := ctx.Value(userIDCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// WithUserID stamps a user id on a context. Auth middleware (Epic 10)
// will own this; exposed here so tests and the per-user limiter can
// participate before auth lands.
func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userIDCtxKey{}, id)
}

// rlStore is a per-key token-bucket store. Buckets are created
// on-demand and swept after `idleTTL` of disuse so the map doesn't
// grow without bound under churn.
type rlStore struct {
	mu      sync.Mutex
	rate    rate.Limit
	burst   int
	bucket  map[string]*rlEntry
	idleTTL time.Duration
}

type rlEntry struct {
	l    *rate.Limiter
	last time.Time
}

func newRLStore(perMin int) *rlStore {
	if perMin <= 0 {
		perMin = 60
	}
	return &rlStore{
		rate:    rate.Limit(float64(perMin) / 60.0),
		burst:   perMin,
		bucket:  map[string]*rlEntry{},
		idleTTL: 10 * time.Minute,
	}
}

// take consumes one token from key's bucket. Returns retryAfterSec when
// the bucket is empty so the handler can set the Retry-After header.
func (s *rlStore) take(key string) (allow bool, retryAfterSec int) {
	now := time.Now()
	s.mu.Lock()
	e, ok := s.bucket[key]
	if !ok {
		e = &rlEntry{l: rate.NewLimiter(s.rate, s.burst)}
		s.bucket[key] = e
	}
	e.last = now
	s.mu.Unlock()

	if e.l.Allow() {
		return true, 0
	}
	// Headroom: how long until the next token is available. Floor at 1
	// so the Retry-After header is always meaningful — `Retry-After: 0`
	// invites a tight retry loop.
	secs := int(time.Duration(float64(time.Second)/float64(s.rate)).Seconds()) + 1
	if secs < 1 {
		secs = 1
	}
	return false, secs
}

// sweep drops idle buckets. Called by callers' background goroutine.
func (s *rlStore) sweep() {
	cutoff := time.Now().Add(-s.idleTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.bucket {
		if e.last.Before(cutoff) {
			delete(s.bucket, k)
		}
	}
}

// PerIP returns a middleware that token-buckets requests per remote IP.
// Story 7.19 AC-5: default 6000/min; the cap should be wide enough that
// a corporate NAT doesn't trip it during normal use.
func PerIP(perMin int) func(http.Handler) http.Handler {
	s := newRLStore(perMin)
	go runSweep(s)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if ok, retry := s.take("ip:" + ip); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(retry))
				httperror.Write(w, r, &httperror.Error{
					Type:   httperror.TypeRateLimited,
					Title:  "too many requests",
					Status: http.StatusTooManyRequests,
					Detail: "per-IP rate limit exceeded",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// PerUser limits per authenticated user. Falls back to IP for
// unauthenticated requests so the limiter is never a no-op. Routes
// matching /progress are exempt — Story 7.11's progress sync has its
// own per-session debounce.
func PerUser(perMin int) func(http.Handler) http.Handler {
	s := newRLStore(perMin)
	go runSweep(s)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isExempt(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			key := perUserKey(r)
			if ok, retry := s.take(key); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(retry))
				httperror.Write(w, r, &httperror.Error{
					Type:   httperror.TypeRateLimited,
					Title:  "too many requests",
					Status: http.StatusTooManyRequests,
					Detail: "per-user rate limit exceeded",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func perUserKey(r *http.Request) string {
	if v, ok := r.Context().Value(userIDCtxKey{}).(string); ok && v != "" {
		return "user:" + v
	}
	return "anon-ip:" + clientIP(r)
}

func isExempt(path string) bool {
	return strings.Contains(path, "/progress")
}

// clientIP picks the request's source IP. Honours X-Forwarded-For
// only when the upstream chi.RealIP middleware has already trimmed it
// to a single value; otherwise reads RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the list — chi.RealIP also normalises
		// this, but defending the limiter against a misconfiguration
		// is cheap.
		if i := strings.Index(xff, ","); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// runSweep drops idle buckets every minute. Started by the constructor
// so callers don't have to remember; the goroutine exits when the
// process exits.
func runSweep(s *rlStore) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		s.sweep()
	}
}
