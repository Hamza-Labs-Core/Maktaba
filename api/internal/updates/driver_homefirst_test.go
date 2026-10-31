package updates

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// Machines update only after the home has committed (the owner's decision, 2026-10-28:
// on that day windows-pc took v0.3.42 while the home still watched it, and the home's
// rollback left it on a release whose schema the home's database no longer had). The
// home goes first: stage, migrations, restart, watch, commit; only its commit lets the
// machines' orders go, by themselves; a home that rolls back or fails orders nothing.

// homeFirstCluster records every command a machine gets, and whether the home had
// committed the run's release when it came.
type homeFirstCluster struct {
	*clusterFake
	home *fakeMachine
	mu   sync.Mutex
	// early: the machine commands sent while the home had not committed
	early []string
	sent  []string
}

func (c *homeFirstCluster) Command(ctx context.Context, sid, name string, args any, d time.Duration) (bool, json.RawMessage, error) {
	if sid != "home-worker" && strings.HasPrefix(name, "update.") && name != "update.status" && name != "update.abort" {
		c.mu.Lock()
		c.sent = append(c.sent, sid+" "+name)
		if c.home.state() != MCommitted {
			c.early = append(c.early, sid+" "+name+" (home "+c.home.state()+")")
		}
		c.mu.Unlock()
	}
	ok, raw, err := c.clusterFake.Command(ctx, sid, name, args, d)
	return ok, raw, err
}

func (c *homeFirstCluster) report() (early, sent []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.early...), append([]string(nil), c.sent...)
}

// homeFirstRig is a driver rig whose home watches its new release until told the outcome
// (the API runs the new release while it watches: Svc.Current), with one machine behind.
func homeFirstRig(t *testing.T) (*driverRig, *homeFirstCluster, *fakeMachine) {
	t.Helper()
	r := newDriverRig(t)
	hc := &homeFirstCluster{clusterFake: r.cluster, home: r.home}
	r.d.Cluster = hc
	n1 := r.node(t, "n1", "windows-pc", "windows-amd64", "1.4.0")
	r.home.pendingFor = 1 << 20 // it watches (a start step) until the test ends it
	r.home.onApply = func() { r.f.svc.Current = "1.5.0" }
	return r, hc, n1
}

// start begins the run as an admin's Update now or as the policy's automatic run.
func startRun(t *testing.T, r *driverRig, trigger string) {
	t.Helper()
	ctx := context.Background()
	switch trigger {
	case TriggerManual:
		if _, err := r.f.svc.RunNow(ctx, "00000000-0000-0000-0000-000000000001", "1.5.0"); err != nil {
			t.Fatal(err)
		}
	default:
		if d, err := r.f.svc.Tick(ctx); err != nil || d.Action != "started" {
			t.Fatalf("tick: %+v %v", d, err)
		}
	}
}

// stepUntil steps the driver (a minute a step) until cond holds, at most n steps.
func stepUntil(t *testing.T, r *driverRig, n int, cond func() bool) {
	t.Helper()
	for i := 0; i < n && !cond(); i++ {
		if err := r.d.Step(context.Background()); err != nil {
			t.Logf("step: %v", err)
		}
		r.f.now = r.f.now.Add(time.Minute)
	}
	if !cond() {
		t.Fatalf("not reached in %d steps (home %s)", n, r.home.state())
	}
}

func homeWatching(r *driverRig) func() bool {
	return func() bool { return r.home.state() == MWatching }
}

// While the home stages, restarts and watches, no machine is ordered anything: the run
// shows them waiting for the home; even a run that gives up on a home that still watches
// (home_timeout) orders none (a catch-up of the release the home only runs, not commits).
// A home that then rolls back leaves every machine on its old release, never ordered.
func TestHomeFirstRollbackOrdersNoMachine(t *testing.T) {
	t.Parallel()
	for _, trigger := range []string{TriggerManual, TriggerAuto} {
		t.Run(trigger, func(t *testing.T) {
			t.Parallel()
			r, hc, n1 := homeFirstRig(t)
			ctx := context.Background()
			startRun(t, r, trigger)
			stepUntil(t, r, 40, homeWatching(r))
			run, _ := r.f.store.ActiveRun(ctx)
			if run == nil {
				t.Fatal("no active run while the home watches")
			}
			// the run shows the machines waiting for the home
			_, det, _ := r.f.store.RunDetailOf(ctx, run.ID)
			if len(det.WaitingHome) != 1 || det.WaitingHome[0].NodeID != "n1" || det.WaitingHome[0].Version != "1.4.0" {
				t.Fatalf("waiting for the home: %+v", det.WaitingHome)
			}
			// the home watches past the run's ceiling: the run gives up on it...
			r.f.now = r.f.now.Add(homeCeiling + time.Hour)
			stepUntil(t, r, 5, func() bool { a, _ := r.f.store.ActiveRun(ctx); return a == nil })
			// ...and the driver goes on looking (catch-ups) while the home still watches
			for i := 0; i < 10; i++ {
				_ = r.d.Step(ctx)
				r.f.now = r.f.now.Add(2 * time.Minute)
			}
			if early, _ := hc.report(); len(early) > 0 {
				t.Fatalf("machines ordered before the home committed: %v", early)
			}
			// the home rolls back: the API is the old release again
			r.home.mu.Lock()
			r.home.pendingFor, r.home.outcome = 0, MRolledBack
			r.home.mu.Unlock()
			r.f.svc.Current = "1.4.0"
			for i := 0; i < 10; i++ {
				_ = r.d.Step(ctx)
				r.f.now = r.f.now.Add(2 * time.Minute)
			}
			if r.home.state() != MRolledBack {
				t.Fatalf("home %s", r.home.state())
			}
			if _, sent := hc.report(); len(sent) > 0 || n1.st != nil || n1.version != "1.4.0" {
				t.Fatalf("a machine was ordered: %v %+v %s", sent, n1.st, n1.version)
			}
			if wm, _ := r.f.store.Watermark(ctx); wm != "" {
				t.Fatalf("watermark %q", wm)
			}
		})
	}
}

// The home's own rollback ends its run; the machines it listed as waiting stay on their
// release, with no target and no order.
func TestHomeFirstHomeRollbackEndsWithMachinesUnordered(t *testing.T) {
	t.Parallel()
	for _, trigger := range []string{TriggerManual, TriggerAuto} {
		t.Run(trigger, func(t *testing.T) {
			t.Parallel()
			r, hc, n1 := homeFirstRig(t)
			ctx := context.Background()
			startRun(t, r, trigger)
			stepUntil(t, r, 40, homeWatching(r))
			r.home.mu.Lock()
			r.home.pendingFor, r.home.outcome = 0, MRolledBack
			r.home.mu.Unlock()
			run := r.drive(t, 20)
			if run.State != RunRolledBack {
				t.Fatalf("run %s", run.State)
			}
			r.f.svc.Current = "1.4.0"
			for i := 0; i < 5; i++ {
				_ = r.d.Step(ctx)
				r.f.now = r.f.now.Add(time.Minute)
			}
			if _, sent := hc.report(); len(sent) > 0 || n1.st != nil {
				t.Fatalf("a machine was ordered: %v %+v", sent, n1.st)
			}
			if ts, _ := r.f.store.Targets(ctx, run.ID); len(ts) != 0 {
				t.Fatalf("targets %+v", ts)
			}
			_, det, _ := r.f.store.RunDetailOf(ctx, run.ID)
			if len(det.WaitingHome) != 1 || det.WaitingHome[0].Name != "windows-pc" {
				t.Fatalf("the machines that waited for the home: %+v", det.WaitingHome)
			}
		})
	}
}

// Once the home commits, the machines' orders go out by themselves (no click) and they
// update; not one command reached a machine before the commit. A home that commits after
// its run gave up on it (home_timeout) is taken up the same way, by a catch-up.
func TestHomeFirstCommitThenMachinesUpdate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		trigger string
		late    bool
	}{{TriggerManual, false}, {TriggerAuto, false}, {TriggerManual, true}, {TriggerAuto, true}} {
		name := tc.trigger
		if tc.late {
			name += "/late"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r, hc, n1 := homeFirstRig(t)
			ctx := context.Background()
			startRun(t, r, tc.trigger)
			stepUntil(t, r, 40, homeWatching(r))
			first, _ := r.f.store.ActiveRun(ctx)
			for i := 0; i < 5; i++ { // the home watches on: nothing goes to a machine
				_ = r.d.Step(ctx)
				r.f.now = r.f.now.Add(time.Minute)
			}
			if _, sent := hc.report(); len(sent) > 0 {
				t.Fatalf("ordered while the home watches: %v", sent)
			}
			if _, det, _ := r.f.store.RunDetailOf(ctx, first.ID); len(det.WaitingHome) != 1 {
				t.Fatalf("the machine is not shown waiting for the home: %+v", det.WaitingHome)
			}
			if tc.late {
				r.f.now = r.f.now.Add(homeCeiling + time.Hour)
				stepUntil(t, r, 5, func() bool { a, _ := r.f.store.ActiveRun(ctx); return a == nil })
				for i := 0; i < 10; i++ { // it still watches: the catch-ups wait for its commit
					_ = r.d.Step(ctx)
					r.f.now = r.f.now.Add(2 * time.Minute)
				}
				if _, sent := hc.report(); len(sent) > 0 {
					t.Fatalf("ordered while the home watches: %v", sent)
				}
			}
			r.home.mu.Lock()
			r.home.pendingFor = 0 // its watch passes: it commits
			r.home.mu.Unlock()
			stepUntil(t, r, 200, func() bool { return n1.state() == MCommitted })
			early, sent := hc.report()
			if len(early) > 0 {
				t.Fatalf("machines ordered before the home committed: %v", early)
			}
			if len(sent) == 0 || n1.version != "1.5.0" {
				t.Fatalf("the machine did not update: %v %s", sent, n1.version)
			}
			if !tc.late {
				_, det, _ := r.f.store.RunDetailOf(ctx, first.ID)
				if len(det.WaitingHome) != 0 {
					t.Fatalf("still waiting for the home after its commit: %+v", det.WaitingHome)
				}
				if ts := targetsByName(t, r.f.store, first.ID); ts["windows-pc"].State != TargetHealthy {
					t.Fatalf("targets %+v", ts)
				}
			}
		})
	}
}

// A rollout of a release the home never committed (a machines-only run: catch-up, retry)
// orders nothing: it ends home_not_committed. The watermark is the home's commit.
func TestHomeFirstNoRolloutWithoutTheHomesCommit(t *testing.T) {
	t.Parallel()
	r, hc, n1 := homeFirstRig(t)
	ctx := context.Background()
	run, err := r.f.store.InsertRunWith(ctx, "1.5.0", "1.4.0", TriggerCatchUp, "", RunDetail{NoHome: true, Targets: []string{"n1"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = r.d.Step(ctx)
	_ = r.d.Step(ctx)
	got, _ := r.f.store.GetRun(ctx, run.ID)
	if got.State != RunFailed || !strings.Contains(string(got.Error), "home_not_committed") {
		t.Fatalf("run %s %s", got.State, got.Error)
	}
	if _, sent := hc.report(); len(sent) > 0 || n1.st != nil {
		t.Fatalf("a machine was ordered: %v %+v", sent, n1.st)
	}
}
