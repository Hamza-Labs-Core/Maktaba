package channel

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestEngineResponsive(t *testing.T) {
	e := NewEngine(nil, nil, nil, 1, nil)
	if err := e.Responsive(context.Background()); err != nil {
		t.Fatalf("idle engine not responsive: %v", err)
	}

	e.mu.Lock() // a wedged engine
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	err := e.Responsive(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wedged engine = %v, want deadline exceeded", err)
	}
	// The first probe is still waiting: the next one must not pile up.
	if err := e.Responsive(context.Background()); !errors.Is(err, errProbePending) {
		t.Fatalf("second probe = %v, want errProbePending", err)
	}
	e.mu.Unlock()

	deadline := time.Now().Add(time.Second)
	for {
		if err := e.Responsive(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("engine never became responsive again after the lock was released")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A probe that answered has let go before it answers: the next one, right
// after it, is never told an earlier probe is still waiting.
func TestEngineResponsiveBackToBack(t *testing.T) {
	e := NewEngine(nil, nil, nil, 1, nil)
	for i := 0; i < 2000; i++ {
		if err := e.Responsive(context.Background()); err != nil {
			t.Fatalf("probe %d on an idle engine: %v", i, err)
		}
		if v, _ := lockProbes.Load(e); v.(*atomic.Bool).Load() {
			t.Fatalf("probe %d answered while still marked pending", i)
		}
	}
}
