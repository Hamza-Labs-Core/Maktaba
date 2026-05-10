package security

import (
	"sync"
	"time"
)

// TokenBucket is a per-key rate limiter. The middleware package wraps
// this for per-IP / per-user limiting; handlers can use it directly for
// per-route limits (e.g. POST /api/pairing/request).
type TokenBucket struct {
	mu       sync.Mutex
	rate     float64 // tokens added per second
	capacity float64 // max tokens
	now      func() time.Time
	state    map[string]*bucketState
}

type bucketState struct {
	tokens   float64
	lastFill time.Time
}

// NewTokenBucket creates a limiter. rate is "tokens per second" and
// capacity is the burst. Both must be > 0.
func NewTokenBucket(ratePerSec, capacity float64) *TokenBucket {
	if ratePerSec <= 0 || capacity <= 0 {
		panic("security: NewTokenBucket requires rate>0 and capacity>0")
	}
	return &TokenBucket{
		rate:     ratePerSec,
		capacity: capacity,
		now:      time.Now,
		state:    map[string]*bucketState{},
	}
}

// Allow returns true if the request for `key` is permitted; consumes
// one token on success.
func (t *TokenBucket) Allow(key string) bool {
	return t.AllowN(key, 1)
}

// AllowN returns true if `n` tokens are available for `key`.
func (t *TokenBucket) AllowN(key string, n float64) bool {
	if n <= 0 {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.state[key]
	now := t.now()
	if !ok {
		st = &bucketState{tokens: t.capacity, lastFill: now}
		t.state[key] = st
	}
	elapsed := now.Sub(st.lastFill).Seconds()
	st.tokens += elapsed * t.rate
	if st.tokens > t.capacity {
		st.tokens = t.capacity
	}
	st.lastFill = now
	if st.tokens < n {
		return false
	}
	st.tokens -= n
	return true
}

// Remaining returns the current token count for `key`. Used by handlers
// that want to populate Retry-After hints.
func (t *TokenBucket) Remaining(key string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.state[key]
	if !ok {
		return t.capacity
	}
	return st.tokens
}

// Reset removes the bucket for `key`. Useful for tests + admin reset endpoints.
func (t *TokenBucket) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.state, key)
}

// Size returns the number of distinct keys tracked. Tests assert
// against this; production has a sweeper goroutine that prunes idle
// keys (not stubbed here).
func (t *TokenBucket) Size() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.state)
}
