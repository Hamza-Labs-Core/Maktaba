package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBreaker_TripsAfterFailures(t *testing.T) {
	b := NewBreaker(30*time.Second, 10*time.Second, 0.5)
	now := time.Now()
	if !b.Allow(now) {
		t.Fatal("breaker must start closed")
	}
	for i := 0; i < 15; i++ {
		b.Record(now, false)
	}
	if b.Allow(now.Add(time.Second)) {
		t.Errorf("breaker must open after threshold breach")
	}
	if !b.Allow(now.Add(11 * time.Second)) {
		t.Errorf("breaker must allow probe after openFor")
	}
}

func TestCallWithRetry_SuccessAfterTransient(t *testing.T) {
	attempts := 0
	err := CallWithRetry(context.Background(), NewBreaker(30*time.Second, 10*time.Second, 0.99), 3, func(_ context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("rpc error: code = UNAVAILABLE")
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("got err=%v attempts=%d", err, attempts)
	}
}

func TestCallWithRetry_NonRetryableImmediate(t *testing.T) {
	attempts := 0
	err := CallWithRetry(context.Background(), NewBreaker(30*time.Second, 10*time.Second, 0.99), 3, func(_ context.Context) error {
		attempts++
		return errors.New("rpc error: code = INVALID_ARGUMENT")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Errorf("non-retryable must not retry; got %d", attempts)
	}
}

func TestCallWithRetry_OpenBreakerFailsFast(t *testing.T) {
	b := NewBreaker(30*time.Second, 10*time.Second, 0.01)
	now := time.Now()
	for i := 0; i < 11; i++ {
		b.Record(now, false)
	}
	err := CallWithRetry(context.Background(), b, 3, func(_ context.Context) error {
		t.Fatal("should not be called")
		return nil
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("got %v want ErrCircuitOpen", err)
	}
}
