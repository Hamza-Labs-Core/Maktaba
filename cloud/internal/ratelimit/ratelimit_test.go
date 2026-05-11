package ratelimit

import (
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
