// Package ratelimit provides an in-memory token-bucket limiter keyed
// per-subject (IP, user id, server id). Backed by sync.Map so a busy
// pod doesn't lock-contend on the limiter itself.
//
// We do not back this with Redis in v1 — single-pod precision is fine
// for the abuse-prevention threshold; the goal is to block bursts, not
// enforce billing-grade quotas (those go through the Meter).
package ratelimit

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Bucket holds the token-bucket state for one subject.
type Bucket struct {
	mu       sync.Mutex
	tokens   float64
	capacity float64
	rate     float64 // tokens per second
	last     time.Time
}

// Take consumes 1 token. Returns true if the request is allowed.
func (b *Bucket) Take(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens = minF(b.capacity, b.tokens+elapsed*b.rate)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// Limiter manages buckets per subject string.
type Limiter struct {
	Capacity float64
	Rate     float64
	now      func() time.Time
	buckets  sync.Map // key -> *Bucket
}

// NewLimiter returns a limiter with the given burst capacity and refill
// rate (per second).
func NewLimiter(capacity, ratePerSec float64) *Limiter {
	return &Limiter{
		Capacity: capacity,
		Rate:     ratePerSec,
		now:      func() time.Time { return time.Now() },
	}
}

// Allow consults the bucket for `key` and returns true if the request
// is allowed.
func (l *Limiter) Allow(key string) bool {
	v, ok := l.buckets.Load(key)
	if !ok {
		b := &Bucket{tokens: l.Capacity, capacity: l.Capacity, rate: l.Rate, last: l.now()}
		actual, _ := l.buckets.LoadOrStore(key, b)
		v = actual
	}
	return v.(*Bucket).Take(l.now())
}

// Middleware wraps an HTTP handler. It pulls the subject key from the
// request via the supplied func — typically client IP for unauth routes
// and user id for authed routes.
func (l *Limiter) Middleware(keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := keyFn(r)
			if k == "" {
				next.ServeHTTP(w, r)
				return
			}
			if !l.Allow(k) {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// IPKey extracts a sensible key from the request — X-Forwarded-For if
// present, RemoteAddr otherwise. The chosen key intentionally falls
// back to RemoteAddr for tests where the header is absent.
func IPKey(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	return r.RemoteAddr
}
