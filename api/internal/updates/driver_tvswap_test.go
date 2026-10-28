package updates

import (
	"context"
	"testing"
	"time"
)

// The owner's rule of 2026-10-28, "tv changes should not block updates": an update always
// starts and drains; only its restart waits for a TV change that swaps (a swap takes
// seconds), and at most TVSwapWait. A change that waits for an approval (ready), or
// anything else in flight, does not hold the restart at all.

// stepTo steps the driver (the clock moving by each step) until cond holds, at most n
// steps.
func (r *driverRig) stepTo(t *testing.T, n int, by time.Duration, what string, cond func() bool) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if cond() {
			return
		}
		if err := r.d.Step(ctx); err != nil {
			t.Logf("step: %v", err)
		}
		r.f.now = r.f.now.Add(by)
	}
	if !cond() {
		t.Fatalf("%s: not within %d steps", what, n)
	}
}

func (r *driverRig) tvChange(t *testing.T, id, state string) {
	t.Helper()
	if _, err := r.f.db.Exec(`INSERT INTO tv_changes (id, number, state, kinds) VALUES ($1, $2, $3, '["lineup"]')`,
		id, len(id), state); err != nil {
		t.Fatal(err)
	}
}

func (r *driverRig) runDetail(t *testing.T) RunDetail {
	t.Helper()
	runs, err := r.f.store.Runs(context.Background(), 1)
	if err != nil || len(runs) == 0 {
		t.Fatalf("runs: %v", err)
	}
	_, det, err := r.f.store.RunDetailOf(context.Background(), runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return det
}

// A swap that ends: the restart waited for it, then went at once.
func TestDriverRestartWaitsForATVSwap(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.tvChange(t, "c-swap", "swapping")
	if d, _ := r.f.svc.Tick(ctx); d.Action != "started" {
		t.Fatalf("an update during a TV swap did not start: %+v", d)
	}
	r.stepTo(t, 40, 10*time.Second, "the restart waits for the swap", func() bool {
		return r.runDetail(t).Waiting == WaitTVSwapping
	})
	if r.home.applies != 0 {
		t.Fatalf("restarted during the swap: %d applies", r.home.applies)
	}
	// only the restart waits: the run started, prepared the home and drained the cluster
	if run := r.active(t); run.State != RunDraining || !r.runDetail(t).HomeDrained {
		t.Fatalf("the run's other steps did not go on: %s %+v", run.State, r.runDetail(t))
	}
	if _, err := r.f.db.Exec(`UPDATE tv_changes SET state = 'live' WHERE id = 'c-swap'`); err != nil {
		t.Fatal(err)
	}
	r.stepTo(t, 3, 10*time.Second, "the restart once the swap ended", func() bool { return r.home.applies == 1 })
	if det := r.runDetail(t); det.Waiting == WaitTVSwapping || det.TVSwapSince != nil {
		t.Fatalf("still waiting: %+v", det)
	}
}

// A swap that does not end: the restart goes on after TVSwapWait (the swap then follows
// its failure rollback), never longer.
func TestDriverRestartWaitsForATVSwapAtMostTwoMinutes(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.tvChange(t, "c-swap", "swapping")
	_, _ = r.f.svc.Tick(ctx)
	r.stepTo(t, 40, 10*time.Second, "the restart waits for the swap", func() bool {
		return r.runDetail(t).Waiting == WaitTVSwapping
	})
	since := *r.runDetail(t).TVSwapSince
	r.stepTo(t, 30, 10*time.Second, "the restart past the wait", func() bool { return r.home.applies == 1 })
	if waited := r.f.now.Sub(since); waited < TVSwapWait || waited > TVSwapWait+30*time.Second {
		t.Fatalf("waited %s for the swap, want %s", waited, TVSwapWait)
	}
}

// An update started while a manual change waits for its approval (ready), or while a
// change is staged or verifying, goes ahead: nothing waits for it, the restart included.
func TestDriverUpdateGoesAheadDuringAReadyTVChange(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"ready", "staged", "verifying", "approved"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			r := newDriverRig(t)
			ctx := context.Background()
			r.f.svc.Blockers = HomeBlockers(r.f.db, "", nil)
			r.tvChange(t, "c-"+state, state)
			if d, _ := r.f.svc.Tick(ctx); d.Action != "started" {
				t.Fatalf("an update during a %s TV change: %+v", state, d)
			}
			r.stepTo(t, 40, 10*time.Second, "the restart", func() bool { return r.home.applies == 1 })
			if det := r.runDetail(t); det.TVSwapSince != nil {
				t.Fatalf("waited for a %s change: %+v", state, det)
			}
		})
	}
}
