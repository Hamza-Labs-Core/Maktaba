package updates

import (
	"context"
	"fmt"
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

// drainedMachines is how many machines are drained now.
func (r *driverRig) drainedMachines() int {
	var n int
	_ = r.f.db.QueryRow(`SELECT count(*) FROM cluster_nodes WHERE drain = 1`).Scan(&n)
	return n
}

// D1 (2026-10-27): an admin's run on a home whose desktop app installs it goes with no
// click there: ordered (nothing drained while the app stages it), then, once staged,
// the cluster drains and the home goes at once (an admin's run: no window, no TV gate).
func TestDriverManualRunHomeAppGoesWithNoClick(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.asks, r.home.appSlow = true, true
	r.viewers = 3 // an admin's run: the TV gate is not its
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	if _, err := r.f.svc.RunNow(ctx, "00000000-0000-0000-0000-000000000001", "1.5.0"); err != nil {
		t.Fatal(err)
	}
	r.stepN(8)
	run := r.active(t)
	if run.State != RunHomeApplying || run.Step != StepWaitingApp || r.home.state() != MOrdered || r.home.applies != 0 {
		t.Fatalf("run %s/%s home %s applies %d", run.State, run.Step, r.home.state(), r.home.applies)
	}
	if n := r.drainedMachines(); n != 0 || len(r.cluster.homeCmds) != 0 {
		t.Fatalf("drained while the app stages it: %d machines, home commands %v", n, r.cluster.homeCmds)
	}
	if k, order := r.home.ordersMade(); k != 1 || order["install_by"] != nil {
		t.Fatalf("orders %d, the last %v (an admin's run names no deadline)", k, order)
	}
	if n := r.f.notes.find(NoticeAppInstalls); n == nil || n.p.Params["machine"] != "home" {
		t.Fatalf("notices %v", r.f.notes.kinds())
	}
	drainedAtGo := false
	r.home.onApply = func() { drainedAtGo = r.drainedMachines() == 1 }
	r.home.stage() // the app staged it: nobody clicks anything
	got := r.drive(t, 60)
	if got.State != RunDone || r.home.applies != 1 || !drainedAtGo {
		t.Fatalf("run %s applies %d drained at the go %v", got.State, r.home.applies, drainedAtGo)
	}
	if strings.Join(r.cluster.homeCmds, ",") != "drain,undrain" {
		t.Fatalf("home worker commands %v", r.cluster.homeCmds)
	}
}

// D1 (2026-10-27): an automatic run on a home whose desktop app installs it keeps its
// window and its TV gate as any home does: ordered with the window's deadline (the app's
// notice), staged, then it waits while the TV is watched and goes with no click once it
// is quiet; a TV watched until the window's end lets the order go (nothing installed).
func TestDriverAutomaticRunHomeAppKeepsItsWindow(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.asks = true
	r.viewers = 3
	if d, err := r.f.svc.Tick(ctx); err != nil || d.Action != "started" {
		t.Fatalf("tick: %+v %v", d, err)
	}
	r.stepN(8)
	run := r.active(t)
	if run.State != RunHomeApplying || run.Step != "waiting_tv" || r.home.state() != MAppStaged || r.home.applies != 0 ||
		r.drainedMachines() != 0 {
		t.Fatalf("run %s/%s home %s applies %d", run.State, run.Step, r.home.state(), r.home.applies)
	}
	_, order := r.home.ordersMade()
	by, _ := time.Parse(time.RFC3339, fmt.Sprint(order["install_by"]))
	if want := time.Date(2026, 10, 22, 7, 0, 0, 0, r.f.now.Location()); !by.Equal(want) {
		t.Fatalf("install by %v, want the window's end and overrun %v", order["install_by"], want)
	}
	if n := r.f.notes.find(NoticeAppInstalls); n == nil || n.p.Params["by"] == nil {
		t.Fatalf("notice %+v (%v)", n, r.f.notes.kinds())
	}
	r.viewers = 0
	got := r.drive(t, 60)
	if got.State != RunDone || r.home.applies != 1 {
		t.Fatalf("run %s applies %d", got.State, r.home.applies)
	}

	r2 := newDriverRig(t)
	r2.home.asks = true
	r2.viewers = 3
	_, _ = r2.f.svc.Tick(ctx)
	r2.stepN(8)
	r2.f.now = time.Date(2026, 10, 22, 7, 30, 0, 0, r2.f.now.Location()) // past the window and its overrun
	got = r2.drive(t, 5)
	if got.State != RunFailed || r2.home.state() != MAborted || r2.home.applies != 0 || r2.drainedMachines() != 0 {
		t.Fatalf("run %s home %s applies %d", got.State, r2.home.state(), r2.home.applies)
	}
	if readErr(got.Error).Code != "tv_busy" {
		t.Fatalf("error %s", got.Error)
	}
}

// D1 (2026-10-27): a machine's desktop app is ordered as soon as it is prepared and
// stages the release while the rollout goes on with the others (the run waits for it,
// step waiting_app); once staged it goes at its turn, one machine at a time, with no
// click there.
func TestDriverMachineAppGoesAtItsTurnWithNoClick(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	a := r.node(t, "n1", "alpha", "darwin-arm64", "1.4.0")
	a.asks, a.appSlow, a.installer = true, true, "appbundle"
	b := r.node(t, "n2", "bravo", "linux-amd64", "1.4.0")
	_, _ = r.f.svc.Tick(ctx)
	r.stepN(40)
	run := r.active(t)
	ts := targetsByName(t, r.f.store, run.ID)
	if run.State != RunRollingOut || run.Step != StepWaitingApp || ts["alpha"].State != TargetOrdered ||
		ts["bravo"].State != TargetHealthy || b.version != "1.5.0" || a.applies != 0 {
		t.Fatalf("run %s/%s alpha %s bravo %s", run.State, run.Step, ts["alpha"].State, ts["bravo"].State)
	}
	if n := r.f.notes.find(NoticeAppInstalls); n == nil || n.p.Params["machine"] != "alpha" {
		t.Fatalf("notices %v", r.f.notes.kinds())
	}
	a.stage()
	got := r.drive(t, 30)
	ts = targetsByName(t, r.f.store, got.ID)
	if got.State != RunDone || ts["alpha"].State != TargetHealthy || a.applies != 1 || r.cluster.overlap {
		t.Fatalf("run %s alpha %s applies %d overlap %v", got.State, ts["alpha"].State, a.applies, r.cluster.overlap)
	}
}

// D1: a run whose rows an API from before the click was removed wrote (step
// waiting_click, the machine's target waiting_click) goes on: the app staged its order,
// and the machine goes at its turn with no click; a machine whose supervisor is from
// before still waits for its own click, and goes at its turn once it was clicked.
func TestDriverTakesUpARunThatWaitedForAClick(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	a := r.node(t, "n1", "alpha", "darwin-arm64", "1.4.0")
	a.asks, a.appSlow = true, true
	old := r.node(t, "n2", "bravo", "darwin-arm64", "1.4.0")
	old.legacy = true
	_, _ = r.f.svc.Tick(ctx)
	r.stepUntil(40, func() bool { return a.state() == MOrdered && old.state() == MAwaitingClick })
	run := r.active(t)
	if _, err := r.f.db.Exec(`UPDATE update_targets SET state = 'waiting_click' WHERE run_id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.f.db.Exec(`UPDATE update_runs SET step = 'waiting_click' WHERE id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	a.stage()
	r.stepUntil(30, func() bool { return a.state() == MCommitted })
	run = r.active(t)
	ts := targetsByName(t, r.f.store, run.ID)
	if a.applies != 1 || ts["alpha"].State != TargetHealthy || ts["bravo"].State != TargetOrdered || run.Step != StepWaitingApp {
		t.Fatalf("run %s/%s alpha %s (%d applies) bravo %s", run.State, run.Step, ts["alpha"].State, a.applies,
			ts["bravo"].State)
	}
	old.click()
	got := r.drive(t, 30)
	ts = targetsByName(t, r.f.store, got.ID)
	if got.State != RunDone || ts["bravo"].State != TargetHealthy || old.applies != 1 || r.cluster.overlap {
		t.Fatalf("run %s bravo %s applies %d", got.State, ts["bravo"].State, old.applies)
	}
}

// D1: a newer release takes the place of a run that only waits for desktop apps to stage
// their orders: the orders are withdrawn and the newer run orders it in their place.
func TestDriverNewerReleaseSupersedesAWaitingRun(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	a := r.node(t, "n1", "alpha", "darwin-arm64", "1.4.0")
	a.asks, a.appSlow = true, true
	_, _ = r.f.svc.Tick(ctx)
	waiting := func(version string) func() bool {
		return func() bool {
			run, _ := r.f.store.ActiveRun(ctx)
			return run != nil && run.Version == version && run.Step == StepWaitingApp && a.state() == MOrdered
		}
	}
	r.stepUntil(40, waiting("1.5.0"))
	old := r.active(t)
	if old.Step != StepWaitingApp || a.state() != MOrdered {
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
	r.stepUntil(40, waiting("1.6.0"))
	if cur := r.active(t); cur.Version != "1.6.0" || a.state() != MOrdered {
		t.Fatalf("new run %s/%s machine %s", cur.Version, cur.Step, a.state())
	}
}

// D1: Cancel withdraws a home's order before its go, the app staging it or staged and
// waiting for the window's gates.
func TestDriverCancelAHomeOrder(t *testing.T) {
	t.Parallel()
	for _, staged := range []bool{false, true} {
		r := newDriverRig(t)
		ctx := context.Background()
		r.home.asks, r.home.appSlow = true, !staged
		r.viewers = 3 // a staged order waits for the TV (an automatic run)
		_, _ = r.f.svc.Tick(ctx)
		r.stepN(8)
		run := r.active(t)
		if want := map[bool]string{false: MOrdered, true: MAppStaged}[staged]; r.home.state() != want {
			t.Fatalf("home %s, want %s", r.home.state(), want)
		}
		if err := r.d.Cancel(ctx, run.ID, "admin"); err != nil {
			t.Fatal(err)
		}
		if got, _ := r.f.store.GetRun(ctx, run.ID); got.State != RunCancelled || r.home.state() != MAborted ||
			r.home.applies != 0 {
			t.Fatalf("staged %v: run %s home %s applies %d", staged, got.State, r.home.state(), r.home.applies)
		}
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
