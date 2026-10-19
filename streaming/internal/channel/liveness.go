package channel

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// lockProbes tracks, per engine, whether a Responsive probe is still
// waiting for the locks, so a wedged engine leaks at most one goroutine.
var lockProbes sync.Map // *Engine -> *atomic.Bool

// errProbePending: an earlier probe is still waiting for the engine lock.
var errProbePending = errors.New("engine lock still held since the previous probe")

// Responsive reports whether the engine's lock and its station registry
// can both be taken before ctx ends: the process watchdog's hard probe
// (Story 22.9). A lock held for good (a deadlock, or a call that never
// returns while holding it) makes every tune hang, and only a restart of
// the process clears it.
func (e *Engine) Responsive(ctx context.Context) error {
	v, _ := lockProbes.LoadOrStore(e, &atomic.Bool{})
	pending := v.(*atomic.Bool)
	if !pending.CompareAndSwap(false, true) {
		return errProbePending
	}
	done := make(chan struct{})
	go func() {
		defer pending.Store(false)
		e.mu.Lock()
		e.mu.Unlock() //nolint:staticcheck // SA2001: taking the lock is the probe
		_ = e.reg.size()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
