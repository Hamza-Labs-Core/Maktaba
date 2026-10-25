package updates

import (
	"context"
	"strings"
	"testing"
	"time"
)

// stepN steps the driver n times, a minute a step.
func (r *driverRig) stepN(n int) {
	ctx := context.Background()
	for i := 0; i < n; i++ {
		_ = r.d.Step(ctx)
		r.f.now = r.f.now.Add(time.Minute)
	}
}

// stepUntil steps the driver until ok holds (at most n steps, a minute of the clock each):
// no more steps than the test needs (the unit tier's time cap).
func (r *driverRig) stepUntil(n int, ok func() bool) {
	ctx := context.Background()
	for i := 0; i < n && !ok(); i++ {
		_ = r.d.Step(ctx)
		r.f.now = r.f.now.Add(time.Minute)
	}
}

func (r *driverRig) active(t *testing.T) Run {
	t.Helper()
	run, err := r.f.store.ActiveRun(context.Background())
	if err != nil || run == nil {
		t.Fatalf("no active run (%v)", err)
	}
	return *run
}

// D1: the home's desktop app is ordered without the window's or the TV's gate and
// nothing is drained while it waits; its click drains the cluster, then the home goes.
func TestDriverHomeAppWaitsForItsClick(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.asks = true
	r.viewers = 3 // a click is the person's choice: the TV gate is not this run's
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	if d, err := r.f.svc.Tick(ctx); err != nil || d.Action != "started" {
		t.Fatalf("tick: %+v %v", d, err)
	}
	r.stepN(8)
	run := r.active(t)
	if run.State != RunHomeApplying || run.Step != StepWaitingClick || r.home.state() != MAwaitingClick || r.home.applies != 0 {
		t.Fatalf("run %s/%s home %s applies %d", run.State, run.Step, r.home.state(), r.home.applies)
	}
	var drained int
	_ = r.f.db.QueryRow(`SELECT count(*) FROM cluster_nodes WHERE drain = 1`).Scan(&drained)
	if drained != 0 || len(r.cluster.homeCmds) != 0 {
		t.Fatalf("drained while waiting for the click: %d machines, home commands %v", drained, r.cluster.homeCmds)
	}
	if r.f.notes.find(NoticeWaitingClick) == nil {
		t.Fatalf("notices %v", r.f.notes.kinds())
	}
	drainedAtGo := false
	r.home.onApply = func() {
		_ = r.f.db.QueryRow(`SELECT count(*) FROM cluster_nodes WHERE drain = 1`).Scan(&drained)
		drainedAtGo = drained == 1
	}
	r.home.click()
	got := r.drive(t, 60)
	if got.State != RunDone || r.home.applies != 1 || !drainedAtGo {
		t.Fatalf("run %s applies %d drained at the go %v", got.State, r.home.applies, drainedAtGo)
	}
	if strings.Join(r.cluster.homeCmds, ",") != "drain,undrain" {
		t.Fatalf("home worker commands %v", r.cluster.homeCmds)
	}
}

// D1: a machine's desktop app waits for its click while the rollout goes on with the
// others; the run stays (waiting_click) and its click takes the next turn.
func TestDriverMachineAppWaitsWhileTheOthersGo(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	a := r.node(t, "n1", "alpha", "darwin-arm64", "1.4.0")
	a.asks, a.installer = true, "appbundle"
	b := r.node(t, "n2", "bravo", "linux-amd64", "1.4.0")
	_, _ = r.f.svc.Tick(ctx)
	r.stepN(40)
	run := r.active(t)
	ts := targetsByName(t, r.f.store, run.ID)
	if run.State != RunRollingOut || run.Step != StepWaitingClick || ts["alpha"].State != TargetWaitingClick ||
		ts["bravo"].State != TargetHealthy || b.version != "1.5.0" || a.applies != 0 {
		t.Fatalf("run %s/%s alpha %s bravo %s", run.State, run.Step, ts["alpha"].State, ts["bravo"].State)
	}
	if r.f.notes.find(NoticeWaitingClick) == nil {
		t.Fatalf("notices %v", r.f.notes.kinds())
	}
	// a catch-up never takes a machine of a run that is still active
	a.click()
	got := r.drive(t, 30)
	ts = targetsByName(t, r.f.store, got.ID)
	if got.State != RunDone || ts["alpha"].State != TargetHealthy || a.applies != 1 || r.cluster.overlap {
		t.Fatalf("run %s alpha %s applies %d overlap %v", got.State, ts["alpha"].State, a.applies, r.cluster.overlap)
	}
}

// D1: a newer release takes the place of a run that only waits for clicks: the orders
// are withdrawn and the newer run orders it in their place.
func TestDriverNewerReleaseSupersedesAWaitingRun(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	a := r.node(t, "n1", "alpha", "darwin-arm64", "1.4.0")
	a.asks = true
	_, _ = r.f.svc.Tick(ctx)
	waiting := func(version string) func() bool {
		return func() bool {
			run, _ := r.f.store.ActiveRun(ctx)
			return run != nil && run.Version == version && run.Step == StepWaitingClick && a.state() == MAwaitingClick
		}
	}
	r.stepUntil(40, waiting("1.5.0"))
	old := r.active(t)
	if old.Step != StepWaitingClick || a.state() != MAwaitingClick {
		t.Fatalf("run %s/%s machine %s", old.State, old.Step, a.state())
	}
	// the run that waits blocks nothing newer
	seed(t, r.f.store, rel("1.6.0", func(x *Release) {
		x.Assets = []Asset{{Name: "release.json", URL: "https://github.test/release.json"}}
	}))
	if d, err := r.f.svc.Tick(ctx); err != nil || d.Action != "started" || d.Version != "1.6.0" {
		t.Fatalf("tick: %+v %v", d, err)
	}
	got, _ := r.f.store.GetRun(ctx, old.ID)
	ts := targetsByName(t, r.f.store, old.ID)
	if got.State != RunSuperseded || a.state() != MAborted || ts["alpha"].State != TargetSkipped {
		t.Fatalf("old run %s machine %s alpha %s", got.State, a.state(), ts["alpha"].State)
	}
	// a clicked machine is not superseded under the person's hand
	r.stepUntil(40, waiting("1.6.0"))
	cur := r.active(t)
	if cur.Version != "1.6.0" || a.state() != MAwaitingClick {
		t.Fatalf("new run %s/%s machine %s", cur.Version, cur.Step, a.state())
	}
	a.click()
	_ = r.d.Step(ctx) // the click noted
	if done, _ := r.d.Supersede(ctx, "1.7.0"); done {
		t.Fatal("a run with a clicked machine was superseded")
	}
}

// D1: Cancel withdraws a home's order before its go, never after.
func TestDriverCancelAHomeOrder(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.asks = true
	_, _ = r.f.svc.Tick(ctx)
	r.stepN(8)
	run := r.active(t)
	if err := r.d.Cancel(ctx, run.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.f.store.GetRun(ctx, run.ID); got.State != RunCancelled || r.home.state() != MAborted {
		t.Fatalf("run %s home %s", got.State, r.home.state())
	}
}

// review 6: a machine the release rolled back on (by itself, or an admin's Roll back) is
// not caught up with it again; review 7: with catch_up "window" a catch-up waits for the
// window (the default, "immediate", catches a machine that keeps no TV up at once:
// TestCatchUpImmediate).
func TestCatchUpRespectsRollbacksAndTheWindow(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	a := r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	a.outcome = MRolledBack
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 40)
	if ts := targetsByName(t, r.f.store, run.ID); ts["alpha"].State != TargetRolledBack {
		t.Fatalf("alpha %s", ts["alpha"].State)
	}
	a.mu.Lock()
	a.st, a.outcome = nil, ""
	a.mu.Unlock()
	_ = r.d.Step(ctx)
	if cu, _ := r.f.store.ActiveRun(ctx); cu != nil {
		t.Fatalf("a catch-up re-applied the release that rolled back: %+v", cu)
	}

	r2 := newDriverRig(t)
	if _, err := r2.f.db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, $2)`, SettingsKey,
		`{"catch_up":"window"}`); err != nil {
		t.Fatal(err)
	}
	n := r2.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	r2.cluster.offline["n1"] = true
	_, _ = r2.f.svc.Tick(ctx)
	r2.drive(t, 30)
	r2.cluster.offline["n1"] = false
	r2.f.now = r2.f.now.Add(6 * time.Hour) // the afternoon: outside 03:00-05:00
	_ = r2.d.Step(ctx)
	if cu, _ := r2.f.store.ActiveRun(ctx); cu != nil || n.st != nil {
		t.Fatalf("a catch-up outside the window: %+v", cu)
	}
	r2.f.now = r2.f.now.Add(18 * time.Hour) // the next night
	_ = r2.d.Step(ctx)
	if cu, _ := r2.f.store.ActiveRun(ctx); cu == nil || cu.Trigger != TriggerCatchUp {
		t.Fatalf("no catch-up in the next window: %+v", cu)
	}
}

// review 8: a machine gone mid-prepare is offline, not a run held for ever.
func TestDriverMachineGoneMidPrepare(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	_, _ = r.f.svc.Tick(ctx)
	for i := 0; i < 40; i++ {
		_ = r.d.Step(ctx)
		r.f.now = r.f.now.Add(time.Minute)
		if run, _ := r.f.store.ActiveRun(ctx); run != nil && run.State == RunRollingOut {
			if ts := targetsByName(t, r.f.store, run.ID); ts["alpha"].State == TargetPreparing {
				r.cluster.offline["n1"] = true
			}
		}
	}
	runs, _ := r.f.store.Runs(ctx, 1)
	ts := targetsByName(t, r.f.store, runs[0].ID)
	if runs[0].State != RunDone || ts["alpha"].State != TargetOffline {
		t.Fatalf("run %s alpha %s", runs[0].State, ts["alpha"].State)
	}
}
