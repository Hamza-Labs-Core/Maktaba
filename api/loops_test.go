package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
)

// T11 (story 51.05): the pairing sweep, the one moved loop that did not take a context
// before, returns as soon as its context ends (its component turned off).
func TestPairingSweepStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runPairingSweep(ctx, discovery.NewMemoryPairingStore(), slog.Default())
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the pairing sweep did not return within its 5 s grace")
	}
}
