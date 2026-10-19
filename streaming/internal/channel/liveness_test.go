package channel

import (
	"context"
	"errors"
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
