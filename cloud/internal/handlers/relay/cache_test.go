package relay

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// countingLookup counts database reads and can fail.
type countingLookup struct {
	reads atomic.Int64
	mu    sync.Mutex
	err   error
	plan  string
}

func (l *countingLookup) BySlug(_ context.Context, slug string) (stores.Server, error) {
	l.reads.Add(1)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return stores.Server{}, l.err
	}
	return stores.Server{ID: "id-" + slug, Slug: slug, Plan: l.plan}, nil
}

func (l *countingLookup) set(err error, plan string) {
	l.mu.Lock()
	l.err, l.plan = err, plan
	l.mu.Unlock()
}

// T01 (story 50.05): within 60 s an entry is served without a read; past it,
// served and refreshed once in the background.
func TestHomeCacheRefreshAhead(t *testing.T) {
	clock := time.Now()
	l := &countingLookup{plan: "pro"}
	c := &HomeCache{Lookup: l, now: func() time.Time { return clock }}
	ctx := context.Background()
	if sv, err := c.BySlug(ctx, "acme"); err != nil || sv.Plan != "pro" {
		t.Fatal(sv, err)
	}
	clock = clock.Add(30 * time.Second)
	_, _ = c.BySlug(ctx, "acme")
	if l.reads.Load() != 1 {
		t.Errorf("reads after a fresh hit: %d", l.reads.Load())
	}
	l.set(nil, "basic")
	clock = clock.Add(31 * time.Second)
	if sv, _ := c.BySlug(ctx, "acme"); sv.Plan != "pro" {
		t.Error("a refresh-ahead hit must serve the cached entry at once")
	}
	_, _ = c.BySlug(ctx, "acme") // a second request while refreshing: no second read
	waitOn(t, "the background refresh", func() bool { sv, _ := c.BySlug(ctx, "acme"); return sv.Plan == "basic" })
	if l.reads.Load() != 2 {
		t.Errorf("reads: %d, want 2 (one refresh)", l.reads.Load())
	}
}

// T02: the database errors: an entry 10 minutes old is served for a home
// served within the hour; a home never seen gets the error; one not served
// for over an hour too. Not found drops the entry.
func TestHomeCacheStaleOnError(t *testing.T) {
	clock := time.Now()
	l := &countingLookup{plan: "pro"}
	c := &HomeCache{Lookup: l, now: func() time.Time { return clock }}
	ctx := context.Background()
	_, _ = c.BySlug(ctx, "acme")
	_, _ = c.BySlug(ctx, "idle")
	down := errors.New("connection refused")
	l.set(down, "")
	clock = clock.Add(10 * time.Minute)
	if sv, err := c.BySlug(ctx, "acme"); err != nil || sv.Plan != "pro" {
		t.Errorf("10 min old, database down: %+v %v", sv, err)
	}
	if _, err := c.BySlug(ctx, "never"); !errors.Is(err, down) {
		t.Errorf("unknown home: %v", err)
	}
	clock = clock.Add(61 * time.Minute)
	if _, err := c.BySlug(ctx, "idle"); !errors.Is(err, down) {
		t.Errorf("not served for over an hour: %v", err)
	}
	l.set(stores.ErrServerNotFound, "")
	if _, err := c.BySlug(ctx, "acme"); !errors.Is(err, stores.ErrServerNotFound) || c.Len() != 1 {
		t.Errorf("not found: %v, %d left", err, c.Len())
	}
}

// The LRU keeps Size entries; Invalidate and Expire.
func TestHomeCacheBoundsAndInvalidation(t *testing.T) {
	clock := time.Now()
	l := &countingLookup{plan: "pro"}
	c := &HomeCache{Lookup: l, Size: 2, now: func() time.Time { return clock }}
	ctx := context.Background()
	for _, s := range []string{"a", "b", "c"} {
		_, _ = c.BySlug(ctx, s)
	}
	if c.Len() != 2 {
		t.Errorf("len %d, want 2", c.Len())
	}
	c.Invalidate("c")
	_, _ = c.BySlug(ctx, "c")
	if l.reads.Load() != 4 {
		t.Errorf("an invalidated entry was not read again: %d", l.reads.Load())
	}
	c.Expire()
	_, _ = c.BySlug(ctx, "c")
	if l.reads.Load() != 5 {
		t.Errorf("an expired entry was not read again: %d", l.reads.Load())
	}
	if !c.Warm() {
		t.Error("warm")
	}
}

// slowLookup never answers until its context ends (a database that vanished:
// connects hang).
type slowLookup struct{ calls atomic.Int64 }

func (l *slowLookup) BySlug(ctx context.Context, _ string) (stores.Server, error) {
	l.calls.Add(1)
	<-ctx.Done()
	return stores.Server{}, ctx.Err()
}

// The rehearsal's db-blip: a known home's request waits at most a second on
// a database that vanished, and for the next 5 s not at all.
func TestHomeCacheDoesNotWaitOnAVanishedDatabase(t *testing.T) {
	clock := time.Now()
	l := &countingLookup{plan: "pro"}
	c := &HomeCache{Lookup: l, now: func() time.Time { return clock }}
	ctx := context.Background()
	_, _ = c.BySlug(ctx, "acme")
	slow := &slowLookup{}
	c.Lookup = slow
	clock = clock.Add(6 * time.Minute) // past its life: a database read is due
	start := time.Now()
	if sv, err := c.BySlug(ctx, "acme"); err != nil || sv.Plan != "pro" {
		t.Fatalf("stale entry: %+v %v", sv, err)
	}
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Errorf("waited %s on the database", took)
	}
	start = time.Now()
	_, _ = c.BySlug(ctx, "acme")
	if took := time.Since(start); took > 100*time.Millisecond || slow.calls.Load() != 1 {
		t.Errorf("the next request waited %s (%d reads): the database is known to be away", took, slow.calls.Load())
	}
}
