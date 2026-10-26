package ratelimit

import (
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiter_AllowsBurst(t *testing.T) {
	l := NewLimiter(5, 1) // burst 5, refill 1/s
	for i := 0; i < 5; i++ {
		if !l.Allow("a") {
			t.Errorf("Allow %d: false, want true", i)
		}
	}
	if l.Allow("a") {
		t.Errorf("Allow 6: true, want false")
	}
}

func TestLimiter_RefillsOverTime(t *testing.T) {
	t0 := time.Now()
	var clock atomic.Int64
	clock.Store(t0.UnixNano())
	l := NewLimiter(2, 1)
	l.now = func() time.Time { return time.Unix(0, clock.Load()) }
	for i := 0; i < 2; i++ {
		if !l.Allow("x") {
			t.Fatalf("burst %d failed", i)
		}
	}
	if l.Allow("x") {
		t.Errorf("burst exceeded should fail")
	}
	clock.Store(t0.Add(2 * time.Second).UnixNano())
	if !l.Allow("x") {
		t.Errorf("after refill: false, want true")
	}
}

// The limiter keys on the TCP peer, not a client-supplied header, and
// ignores the source port.
func TestIPKeyIgnoresSpoofedHeaderAndPort(t *testing.T) {
	a := httptest.NewRequest("POST", "/v1/auth/login", nil)
	a.RemoteAddr = "203.0.113.5:1111"
	a.Header.Set("X-Forwarded-For", "1.2.3.4")
	b := httptest.NewRequest("POST", "/v1/auth/login", nil)
	b.RemoteAddr = "203.0.113.5:2222"
	b.Header.Set("X-Forwarded-For", "5.6.7.8")
	if IPKey(a) != IPKey(b) || IPKey(a) != "ip:203.0.113.5" {
		t.Fatalf("keys %q %q", IPKey(a), IPKey(b))
	}
}

// Idle buckets that have refilled are dropped, so one-off addresses do
// not grow the map forever.
func TestLimiterSweepsIdleBuckets(t *testing.T) {
	t0 := time.Now()
	var clock atomic.Int64
	clock.Store(t0.UnixNano())
	l := NewLimiter(2, 1)
	l.now = func() time.Time { return time.Unix(0, clock.Load()) }
	for i := 0; i < 100; i++ {
		l.Allow(fmt.Sprintf("k%d", i))
	}
	clock.Store(t0.Add(2 * time.Minute).UnixNano())
	l.Allow("fresh")
	if n := l.Len(); n != 1 {
		t.Fatalf("buckets after sweep: %d", n)
	}
}
