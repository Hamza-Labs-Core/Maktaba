package main

import (
	"context"
	"sync"
	"time"
)

// background owns the long-running goroutines `serve` and `slot` start (the
// relay, heartbeats, placement and failover watchers, the slots, SSDP, GC...).
// They all run on one context; shutdown cancels it and waits for them, bounded,
// before the database pool closes. pgxpool's Close blocks while a connection is
// out, so one loop still holding its LISTEN connection would hang the exit.
type background struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu    sync.Mutex
	stops []func()
}

func newBackground(parent context.Context) *background {
	ctx, cancel := context.WithCancel(parent)
	return &background{ctx: ctx, cancel: cancel}
}

// Go runs fn on its own goroutine with the shared context; Stop waits for it.
// Once Stop has begun it does nothing (the context is already done).
func (b *background) Go(fn func(ctx context.Context)) {
	b.mu.Lock()
	if b.ctx.Err() != nil {
		b.mu.Unlock()
		return
	}
	b.wg.Add(1)
	b.mu.Unlock()
	go func() {
		defer b.wg.Done()
		fn(b.ctx)
	}()
}

// OnStop runs fn at Stop, after the context is cancelled (e.g. stopping the
// channel engine's stations).
func (b *background) OnStop(fn func()) {
	b.mu.Lock()
	b.stops = append(b.stops, fn)
	b.mu.Unlock()
}

// Stop cancels the context, runs the stop hooks and waits up to timeout for
// every goroutine. False when some are still running: the caller must then not
// close what they use (the pool), or the exit hangs.
func (b *background) Stop(timeout time.Duration) bool {
	b.mu.Lock()
	b.cancel() // under mu: no Go adds to wg once Wait may run
	stops := b.stops
	b.stops = nil
	b.mu.Unlock()
	done := make(chan struct{})
	go func() {
		for _, fn := range stops {
			fn()
		}
		b.wg.Wait()
		close(done)
	}()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

// tick calls fn every d until ctx ends.
func tick(ctx context.Context, d time.Duration, fn func()) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

// shutdownWait bounds how long shutdown waits for the background goroutines.
const shutdownWait = 5 * time.Second
