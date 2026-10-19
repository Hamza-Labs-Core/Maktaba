package channel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeFence is a placement table of one host ("self").
type fakeFence struct {
	mu       sync.Mutex
	gen      map[uuid.UUID]int64 // current generation naming self (0: not self)
	takeover bool
	refuse   error
	acked    []int64
	released []int64
}

func (f *fakeFence) Allow(_ context.Context, ch uuid.UUID) (Grant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse != nil {
		return Grant{}, f.refuse
	}
	if f.gen[ch] == 0 {
		return Grant{}, &RunningElsewhereError{HostID: "node-1"}
	}
	return Grant{Gen: f.gen[ch], Discontinuity: f.takeover}, nil
}

func (f *fakeFence) Check(_ context.Context, ch uuid.UUID, gen int64) (bool, int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.gen[ch]
	if g == 0 {
		return false, 0
	}
	if g != gen {
		return true, g
	}
	return true, 0
}

func (f *fakeFence) Ack(_ context.Context, _ uuid.UUID, gen int64) {
	f.mu.Lock()
	f.acked = append(f.acked, gen)
	f.mu.Unlock()
}

func (f *fakeFence) Release(_ context.Context, _ uuid.UUID, gen int64) {
	f.mu.Lock()
	f.released = append(f.released, gen)
	f.mu.Unlock()
}

func TestFenceRefusingStartsNothing(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	e, _, sc := newTestEngine(t, clk, 0)
	e.Fence = &fakeFence{gen: map[uuid.UUID]int64{}}
	if _, err := e.Ensure(context.Background(), uuid.New()); !errors.Is(err, ErrRunningElsewhere) {
		t.Fatalf("err = %v", err)
	}
	if sc.n != 0 {
		t.Fatal("a fenced channel must not start")
	}
}

func TestFenceAcksAndPassesDiscontinuity(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	repo := &fakeRepo{}
	var got StartOptions
	start := func(ctx context.Context, _ uuid.UUID, dir string) (Pipeline, error) {
		got, _ = StartOptionsFrom(ctx)
		return newFakePipeline(dir, 0), nil
	}
	e := NewEngine(repo, fakeLayout{root: t.TempDir()}, start, 2, clk.now)
	t.Cleanup(e.StopAll)
	id := uuid.New()
	f := &fakeFence{gen: map[uuid.UUID]int64{id: 7}, takeover: true}
	e.Fence = f
	if _, err := e.Ensure(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if !got.Discontinuity {
		t.Error("a takeover start must ask for an HLS discontinuity")
	}
	if len(f.acked) != 1 || f.acked[0] != 7 {
		t.Errorf("acked %v, want [7]", f.acked)
	}
}

// TC6 of 32.2: a superseded generation stops the station within one check.
func TestFenceStopsSupersededStation(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	e, _, sc := newTestEngine(t, clk, 0)
	id := uuid.New()
	f := &fakeFence{gen: map[uuid.UUID]int64{id: 3}}
	e.Fence = f
	if _, err := e.Ensure(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	// a newer generation for this host: adopted, the station keeps running
	f.mu.Lock()
	f.gen[id] = 4
	f.mu.Unlock()
	e.CheckFences(context.Background())
	if !sc.last.Alive() {
		t.Fatal("a newer placement naming this host must not stop the station")
	}
	// moved to another host: stopped
	f.mu.Lock()
	f.gen[id] = 0
	f.mu.Unlock()
	e.CheckFences(context.Background(), id)
	select {
	case <-sc.last.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("superseded station still running")
	}
}

// TC7 of 32.2: a reaped station releases only its own generation.
func TestReleaseOnReap(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	e, _, _ := newTestEngine(t, clk, 0)
	id := uuid.New()
	f := &fakeFence{gen: map[uuid.UUID]int64{id: 11}}
	e.Fence = f
	if _, err := e.Ensure(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	clk.advance(2 * DefaultWarmGrace)
	if got := e.ReapIdle(context.Background()); len(got) != 1 {
		t.Fatalf("reaped %v", got)
	}
	if len(f.released) != 1 || f.released[0] != 11 {
		t.Errorf("released %v, want [11]", f.released)
	}
	// a second reap releases nothing more
	e.ReapIdle(context.Background())
	if len(f.released) != 1 {
		t.Errorf("released twice: %v", f.released)
	}
}
