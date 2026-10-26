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
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
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

	lastSweep atomic.Int64
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
	l.maybeSweep()
	v, ok := l.buckets.Load(key)
	if !ok {
		b := &Bucket{tokens: l.Capacity, capacity: l.Capacity, rate: l.Rate, last: l.now()}
		actual, _ := l.buckets.LoadOrStore(key, b)
		v = actual
	}
	return v.(*Bucket).Take(l.now())
}

// sweepEvery bounds how often idle buckets are dropped. A bucket that
// has been idle long enough to refill completely is indistinguishable
// from a fresh one, so dropping it changes no decision — it only keeps
// a stream of one-off client addresses from growing the map forever.
const sweepEvery = time.Minute

func (l *Limiter) maybeSweep() {
	now := l.now()
	last := l.lastSweep.Load()
	if now.UnixNano()-last < int64(sweepEvery) || !l.lastSweep.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	full := time.Duration(l.Capacity / l.Rate * float64(time.Second))
	l.buckets.Range(func(k, v any) bool {
		b := v.(*Bucket)
		b.mu.Lock()
		idle := now.Sub(b.last)
		b.mu.Unlock()
		if idle > full {
			l.buckets.Delete(k)
		}
		return true
	})
}

// Len is the number of tracked buckets, for tests.
func (l *Limiter) Len() int {
	n := 0
	l.buckets.Range(func(any, any) bool { n++; return true })
	return n
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
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", strconv.Itoa(l.retryAfter()))
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// retryAfter is the whole seconds until one token refills.
func (l *Limiter) retryAfter() int {
	if l.Rate <= 0 {
		return 60
	}
	s := int(1/l.Rate + 0.999)
	if s < 1 {
		s = 1
	}
	return s
}

// IPKey keys a request by its client address as resolved by
// middleware.WithClientIP (the TCP peer, or the proxy header only when
// the deployment trusts its proxy). It never reads a client-supplied
// header itself: a spoofed X-Forwarded-For would otherwise mint a fresh
// bucket per request. Without the middleware it falls back to the peer
// address with the port stripped.
func IPKey(r *http.Request) string {
	if ip := middleware.GetClientIP(r.Context()); ip != "" {
		return "ip:" + ip
	}
	return "ip:" + middleware.ClientIP(r, false)
}
