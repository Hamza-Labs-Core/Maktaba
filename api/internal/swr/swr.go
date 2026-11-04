// Package swr is a stale-while-revalidate cache of one value with a single
// build in flight.
//
// A read that finds a fresh value returns it. A read that finds a stale value
// returns it at once and starts one rebuild in the background (later readers
// share it). Only a read with nothing cached waits, for that one build, and no
// longer than its own context. A build runs on its own context (the caller's
// values, not its cancellation, with Timeout): a reader that goes away, or the
// cluster hub's 10 s deadline, never cancels the statement half way, so the
// next reader finds its result. A failed build keeps the last value.
package swr

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultTimeout bounds one build when Cache.Timeout is zero.
const DefaultTimeout = 2 * time.Minute

// DefaultFreshWait is how long a read after Invalidate waits for the new value
// before it returns the old one, when Cache.FreshWait is zero.
const DefaultFreshWait = 3 * time.Second

// Cache holds one value of T. The zero value is ready to use (fresh for
// nothing: every read past the first starts a background rebuild); set TTL.
type Cache[T any] struct {
	// TTL is how long a value is fresh.
	TTL time.Duration
	// Timeout bounds one build (DefaultTimeout when zero).
	Timeout time.Duration
	// FreshWait bounds how long a read after Invalidate waits for the new
	// value (DefaultFreshWait when zero).
	FreshWait time.Duration
	// OnError is told of a build that failed (it may be nil).
	OnError func(error)

	mu     sync.Mutex
	val    T
	at     time.Time
	has    bool
	dirty  bool
	gen    int // raised by Invalidate
	flight *flight
	builds int
}

type flight struct {
	done chan struct{}
	err  error
	gen  int
}

// Get returns the cached value as of now, building it with build when there
// is none, or refreshing it when it is older than TTL (see the package doc).
func (c *Cache[T]) Get(ctx context.Context, now time.Time, build func(context.Context) (T, error)) (T, error) {
	c.mu.Lock()
	if c.has && !c.dirty && now.Sub(c.at) < c.TTL {
		v := c.val
		c.mu.Unlock()
		return v, nil
	}
	f := c.startLocked(ctx, now, build)
	if c.has && !c.dirty {
		v := c.val
		c.mu.Unlock()
		return v, nil
	}
	hadStale := c.has
	c.mu.Unlock()

	// invalidated: wait a little for the new value, then serve the old one
	var timer *time.Timer
	var timeout <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	armed := func() {
		if timer == nil {
			timer = time.NewTimer(c.freshWait())
			timeout = timer.C
		}
	}
	if hadStale {
		armed()
	}
	for {
		select {
		case <-f.done:
		case <-timeout:
			return c.current()
		case <-ctx.Done():
			if v, err := c.current(); err == nil {
				return v, nil
			}
			var zero T
			return zero, ctx.Err()
		}
		c.mu.Lock()
		switch {
		case c.has && c.dirty:
			// a write came while that build ran: one more, for the new value
			f = c.startLocked(ctx, now, build)
			c.mu.Unlock()
			armed()
			continue
		case c.has:
			v := c.val
			c.mu.Unlock()
			return v, nil
		}
		c.mu.Unlock()
		var zero T
		return zero, f.err
	}
}

// Peek never waits: it returns the cached value (fresh or stale) and whether
// there is one, starting a build when there is none or it is stale. For a part
// of a larger read that may go without it until it is ready.
func (c *Cache[T]) Peek(ctx context.Context, now time.Time, build func(context.Context) (T, error)) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.has || c.dirty || now.Sub(c.at) >= c.TTL {
		c.startLocked(ctx, now, build)
	}
	return c.val, c.has
}

// current is the cached value, or errNone when there is none.
func (c *Cache[T]) current() (T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.has {
		var zero T
		return zero, errNone
	}
	return c.val, nil
}

var errNone = errors.New("swr: no value yet")

// Invalidate marks the value stale after a write that changes it: the next
// read waits (up to FreshWait) for a new one instead of returning it.
func (c *Cache[T]) Invalidate() {
	c.mu.Lock()
	c.gen++
	if c.has {
		c.dirty = true
	}
	c.mu.Unlock()
}

// Wait blocks until no build is in flight (or ctx is done); for tests and
// shutdown.
func (c *Cache[T]) Wait(ctx context.Context) error {
	c.mu.Lock()
	f := c.flight
	c.mu.Unlock()
	if f == nil {
		return nil
	}
	select {
	case <-f.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Builds is how many builds have started (tests).
func (c *Cache[T]) Builds() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.builds
}

// startLocked starts a build unless one is in flight; c.mu is held.
func (c *Cache[T]) startLocked(ctx context.Context, now time.Time, build func(context.Context) (T, error)) *flight {
	if c.flight != nil {
		return c.flight
	}
	f := &flight{done: make(chan struct{}), gen: c.gen}
	c.flight = f
	c.builds++
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	go func() {
		defer cancel()
		v, err := runBuild(bctx, build)
		c.mu.Lock()
		if err == nil {
			// a write after the build began leaves the value stale
			c.val, c.at, c.has, c.dirty = v, now, true, f.gen != c.gen
		}
		f.err = err
		c.flight = nil
		onErr := c.OnError
		c.mu.Unlock()
		close(f.done)
		if err != nil && onErr != nil {
			onErr(err)
		}
	}()
	return f
}

// runBuild runs build, turning a panic into an error so a waiter is never
// left hanging.
func runBuild[T any](ctx context.Context, build func(context.Context) (T, error)) (v T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &panicError{r}
		}
	}()
	return build(ctx)
}

type panicError struct{ v any }

func (p *panicError) Error() string { return fmt.Sprintf("swr: build panicked: %v", p.v) }

func (c *Cache[T]) freshWait() time.Duration {
	if c.FreshWait > 0 {
		return c.FreshWait
	}
	return DefaultFreshWait
}
