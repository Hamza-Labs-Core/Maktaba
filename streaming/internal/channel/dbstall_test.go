package channel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stallRepo is a repo whose database calls block until released (a hung
// Postgres connection).
type stallRepo struct {
	fakeRepo
	gate chan struct{}
}

func (r *stallRepo) Look(ctx context.Context, id uuid.UUID) (Look, error) {
	select {
	case <-r.gate:
	case <-ctx.Done():
		return Look{}, ctx.Err()
	}
	return r.fakeRepo.Look(ctx, id)
}

func (r *stallRepo) SetRuntime(ctx context.Context, rt Runtime) error {
	select {
	case <-r.gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	return r.fakeRepo.SetRuntime(ctx, rt)
}

// A start stuck on the database (and runtime rows stuck behind it) never holds
// the engine lock: the liveness probe answers, other channels already on air are
// served, and the start completes when the database answers.
func TestEnsureDatabaseStallKeepsTheLockFree(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	repo := &stallRepo{gate: make(chan struct{})}
	sc := &startCounter{}
	e := NewEngine(repo, fakeLayout{root: t.TempDir()}, sc.start, 4, clk.now)
	t.Cleanup(e.StopAll)

	done := make(chan error, 1)
	stalled := uuid.New()
	go func() {
		_, err := e.Ensure(context.Background(), stalled)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond) // the start is waiting on the database
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		err := e.Responsive(ctx)
		cancel()
		if err != nil {
			t.Fatalf("liveness probe while the database stalls: %v", err)
		}
	}
	syncDone := make(chan struct{})
	go func() { e.SyncRuntime(context.Background()); close(syncDone) }()
	select {
	case <-syncDone: // queues, never waits for the database
	case <-time.After(time.Second):
		t.Fatal("SyncRuntime waited on the database")
	}
	// a second request for the stalled channel waits for that start (one start)
	second := make(chan error, 1)
	go func() {
		_, err := e.Ensure(context.Background(), stalled)
		second <- err
	}()
	close(repo.gate)
	for _, ch := range []chan error{done, second} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatalf("start after the stall: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("the start did not complete")
		}
	}
	if sc.n != 1 {
		t.Fatalf("%d starts for one channel", sc.n)
	}
	if !e.FlushRuntime(2*time.Second) || repo.setCalls == 0 {
		t.Fatalf("runtime row not written after the stall (%d)", repo.setCalls)
	}
}

// genFence hands out a new generation per grant and records releases.
type genFence struct {
	mu       sync.Mutex
	next     int64
	cur      map[uuid.UUID]int64
	released map[int64]bool
}

func (f *genFence) Allow(_ context.Context, ch uuid.UUID) (Grant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.cur[ch] = f.next
	return Grant{Gen: f.next}, nil
}
func (f *genFence) Check(context.Context, uuid.UUID, int64) (bool, int64) { return true, 0 }
func (f *genFence) Ack(context.Context, uuid.UUID, int64)                 {}
func (f *genFence) Release(_ context.Context, _ uuid.UUID, gen int64) {
	f.mu.Lock()
	f.released[gen] = true
	f.mu.Unlock()
}

// The reap never releases the placement of a station that started while it ran:
// tunes and reaps race, and no running station's generation is ever released.
func TestReapNeverReleasesANewStation(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	repo := &fakeRepo{}
	sc := &startCounter{}
	e := NewEngine(repo, fakeLayout{root: t.TempDir()}, sc.start, 8, clk.now)
	e.WarmGrace = 0
	f := &genFence{cur: map[uuid.UUID]int64{}, released: map[int64]bool{}}
	e.Fence = f
	t.Cleanup(e.StopAll)
	ch := uuid.New()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = e.Ensure(context.Background(), ch)
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			clk.advance(time.Second)
			e.ReapIdle(context.Background())
		}
	}()
	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
	e.mu.Lock()
	gen, running := e.gens[ch]
	p := e.stations[ch]
	e.mu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	if running && p != nil && p.Alive() && f.released[gen] {
		t.Fatalf("the running station's generation %d was released (of %d grants)", gen, f.next)
	}
	if len(f.released) == 0 {
		t.Fatal("no reap happened")
	}
}
