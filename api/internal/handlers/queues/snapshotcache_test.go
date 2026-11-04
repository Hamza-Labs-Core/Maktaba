package queues

import (
	"context"
	"database/sql/driver"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

// withHook puts hook on the snapshot rule whose Match is match.
func withHook(rules []*queuestest.Rule, match string, hook func() error) []*queuestest.Rule {
	for _, r := range rules {
		if r.Match == match {
			r.Hook = hook
		}
	}
	return rules
}

const liveCountsMatch = "min(created_at) filter"

// GET /api/queues, the /ws/queues publisher and the cluster hub read one
// snapshot: any number of readers at once run its queries once (on the live
// home each request ran them all, 18-20 s, and the hub's 10 s deadline
// cancelled its statements every 10 s).
func TestSnapshot_ConcurrentReadersShareOneBuild(t *testing.T) {
	gate := make(chan struct{})
	f := newFixture(withHook(snapshotRules(), liveCountsMatch, func() error { <-gate; return nil })...)
	var wg sync.WaitGroup
	codes := make([]int, 10)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = f.do(http.MethodGet, "/api/queues", "", admin()).Code
		}(i)
	}
	// and the hub, with its own deadline, at the same time
	hubErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := f.h.BuildSnapshot(ctx)
		hubErr <- err
	}()
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	for i, c := range codes {
		if c != 200 {
			t.Fatalf("reader %d: %d", i, c)
		}
	}
	if err := <-hubErr; err != nil {
		t.Fatal(err)
	}
	if n := len(f.db.Find(liveCountsMatch)); n != 1 {
		t.Fatalf("11 concurrent readers ran the live counts %d times, want 1", n)
	}
}

// The hub's deadline ends its wait, not the build: the next tick finds it.
func TestSnapshot_HubDeadlineDoesNotCancelTheBuild(t *testing.T) {
	release := make(chan struct{})
	f := newFixture(withHook(snapshotRules(), liveCountsMatch, func() error {
		<-release
		return nil
	})...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := f.h.BuildSnapshot(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first tick: %v", err)
	}
	close(release)
	if err := f.h.waitReads(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap, err := f.h.BuildSnapshot(context.Background())
	if err != nil || len(snap.Queues) != 4 {
		t.Fatalf("next tick: %d queues, %v", len(snap.Queues), err)
	}
	if n := len(f.db.Find(liveCountsMatch)); n != 1 {
		t.Fatalf("live counts ran %d times, want 1", n)
	}
}

// A rebuild that fails (a cancelled or timed-out statement) serves the last
// snapshot: the page never gets a 500 while it has one.
func TestSnapshot_FailedRebuildServesTheLastOne(t *testing.T) {
	var fail atomic.Bool
	f := newFixture(withHook(snapshotRules(), liveCountsMatch, func() error {
		if fail.Load() {
			return errors.New("pq: canceling statement due to user request")
		}
		return nil
	})...)
	if rec := f.do(http.MethodGet, "/api/queues", "", admin()); rec.Code != 200 {
		t.Fatalf("first: %d", rec.Code)
	}
	fail.Store(true)
	now := t0.Add(time.Minute)
	f.h.NowFunc = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		rec := f.do(http.MethodGet, "/api/queues", "", admin())
		var snap Snapshot
		decode(t, rec, &snap)
		if rec.Code != 200 || snap.Queues[0].Counts.Pending != 10 {
			t.Fatalf("read %d during failing rebuilds: %d %s", i, rec.Code, rec.Body.String())
		}
		if err := f.h.waitReads(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

// A control action (here Pause all) marks the snapshot stale: the next read
// shows it, without waiting out the cache.
func TestSnapshot_ControlActionShowsAtOnce(t *testing.T) {
	rules := snapshotRules()
	var control *queuestest.Rule
	for _, r := range rules {
		if r.Match == "from queue_control" {
			control = r
		}
	}
	f := newFixture(rules...)
	var snap Snapshot
	decode(t, f.do(http.MethodGet, "/api/queues", "", admin()), &snap)
	if snap.PausedAll {
		t.Fatal("paused before the pause")
	}
	if err := f.h.waitReads(context.Background()); err != nil {
		t.Fatal(err)
	}
	control.Rows = [][]driver.Value{{true}} // what the database says after the pause
	if rec := f.do(http.MethodPost, "/api/queues/pause-all", "", admin()); rec.Code != http.StatusNoContent {
		t.Fatalf("pause all: %d %s", rec.Code, rec.Body.String())
	}
	decode(t, f.do(http.MethodGet, "/api/queues", "", admin()), &snap)
	if !snap.PausedAll {
		t.Fatal("the read after Pause all still shows the old snapshot")
	}
}
