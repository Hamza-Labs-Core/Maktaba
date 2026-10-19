package watchdog

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTest() (*Watchdog, *clock) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	w := New()
	w.now = c.now
	w.lastBeat = c.now()
	w.ProbeTimeout = 50 * time.Millisecond
	return w, c
}

func TestHealthyBeatsKeepItLive(t *testing.T) {
	t.Parallel()
	w, c := newTest()
	w.Watch("engine", func(context.Context) error { return nil })
	for i := 0; i < 5; i++ {
		c.add(30 * time.Second)
		w.Beat(context.Background())
	}
	if err := w.LiveCheck().Run(context.Background()); err != nil {
		t.Fatalf("live = %v", err)
	}
}

// TC7: a wedged engine probe flips liveness after the stale window.
func TestWedgedProbeFailsLivenessAfterStale(t *testing.T) {
	t.Parallel()
	w, c := newTest()
	w.Watch("channel_engine", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	w.Beat(context.Background())
	c.add(30 * time.Second)
	if err := w.LiveCheck().Run(context.Background()); err != nil {
		t.Fatalf("live before the stale window = %v", err)
	}
	c.add(20 * time.Second)
	w.Beat(context.Background())
	err := w.LiveCheck().Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "channel_engine") {
		t.Fatalf("live after stale = %v, want the engine named", err)
	}
	if err := w.ReadyCheck("channel_engine").Run(context.Background()); err == nil {
		t.Error("readiness should report the failing engine probe")
	}
}

// A stuck beat loop (no beats at all) also fails liveness.
func TestNoBeatsFailLiveness(t *testing.T) {
	t.Parallel()
	w, c := newTest()
	c.add(DefaultStale + time.Second)
	if err := w.LiveCheck().Run(context.Background()); err == nil {
		t.Fatal("no beat for longer than Stale must fail liveness")
	}
}

// A failing soft probe (the database) never fails liveness.
func TestSoftProbeOnlyAffectsReadiness(t *testing.T) {
	t.Parallel()
	w, c := newTest()
	w.Observe("db", func(context.Context) error { return errors.New("connection refused") })
	for i := 0; i < 5; i++ {
		c.add(30 * time.Second)
		w.Beat(context.Background())
	}
	if err := w.LiveCheck().Run(context.Background()); err != nil {
		t.Fatalf("db outage failed liveness: %v", err)
	}
	if err := w.ReadyCheck("db").Run(context.Background()); err == nil {
		t.Fatal("db outage not reported by readiness")
	}
}

func TestRunStopsWithContext(t *testing.T) {
	t.Parallel()
	w := New()
	w.Interval = time.Millisecond
	var mu sync.Mutex
	beats := 0
	w.Watch("x", func(context.Context) error {
		mu.Lock()
		beats++
		mu.Unlock()
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
	mu.Lock()
	defer mu.Unlock()
	if beats < 2 {
		t.Errorf("beats = %d", beats)
	}
}
