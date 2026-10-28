package updates

import (
	"context"
	"strings"
	"testing"
	"time"
)

// review A2: a home that neither commits nor rolls back within hours of its switch ends
// the run (the cluster undrained, a person told), whatever its supervisor answers.
func TestDriverHomeCeiling(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	r.home.outcome = "silent"
	_, _ = r.f.svc.Tick(ctx)
	r.stepN(8)
	run := r.active(t)
	if run.State != RunHomeApplying {
		t.Fatalf("run %s", run.State)
	}
	var drained int
	_ = r.f.db.QueryRow(`SELECT count(*) FROM cluster_nodes WHERE drain = 1`).Scan(&drained)
	if drained != 1 {
		t.Fatalf("drained %d", drained)
	}
	r.f.now = r.f.now.Add(homeCeiling + time.Minute)
	_ = r.d.Step(ctx)
	got, _ := r.f.store.GetRun(ctx, run.ID)
	if got.State != RunFailed || !strings.Contains(string(got.Error), "home_timeout") || r.f.notes.find(NoticeNeedsPerson) == nil {
		t.Fatalf("run %s %s notices %v", got.State, got.Error, r.f.notes.kinds())
	}
	_ = r.f.db.QueryRow(`SELECT count(*) FROM cluster_nodes WHERE drain = 1`).Scan(&drained)
	if drained != 0 {
		t.Fatalf("still drained: %d", drained)
	}
}

// review A3: Cancel during a rollout withdraws the orders before their go.
func TestDriverCancelWithdrawsWaitingOrders(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	a := r.node(t, "n1", "alpha", "darwin-arm64", "1.4.0")
	a.asks, a.appSlow = true, true
	_, _ = r.f.svc.Tick(ctx)
	r.stepN(40)
	run := r.active(t)
	if a.state() != MOrdered {
		t.Fatalf("machine %s", a.state())
	}
	if err := r.d.Cancel(ctx, run.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.f.store.GetRun(ctx, run.ID); got.State != RunCancelled || a.state() != MAborted {
		t.Fatalf("run %s machine %s", got.State, a.state())
	}
}

// review A4: a machine offline when the rollout began, back while the run waits for
// another machine's app to stage its order, is updated by that run (no catch-up can
// start meanwhile).
func TestDriverRolloutTakesBackAMachine(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	a := r.node(t, "n1", "alpha", "darwin-arm64", "1.4.0")
	a.asks, a.appSlow = true, true
	b := r.node(t, "n2", "bravo", "linux-amd64", "1.4.0")
	r.cluster.offline["n2"] = true
	_, _ = r.f.svc.Tick(ctx)
	// only the steps it takes (the unit tier's time cap)
	r.stepUntil(40, func() bool {
		run, _ := r.f.store.ActiveRun(ctx)
		return run != nil && run.Step == StepWaitingApp &&
			targetsByName(t, r.f.store, run.ID)["bravo"].State == TargetOffline
	})
	run := r.active(t)
	if ts := targetsByName(t, r.f.store, run.ID); run.Step != StepWaitingApp || ts["bravo"].State != TargetOffline {
		t.Fatalf("run %s/%s bravo %s", run.State, run.Step, ts["bravo"].State)
	}
	r.cluster.offline["n2"] = false
	r.stepUntil(20, func() bool {
		return targetsByName(t, r.f.store, run.ID)["bravo"].State == TargetHealthy && b.version == "1.5.0"
	})
	if ts := targetsByName(t, r.f.store, run.ID); ts["bravo"].State != TargetHealthy || b.version != "1.5.0" {
		t.Fatalf("bravo %s on %s", ts["bravo"].State, b.version)
	}
}

// review A5: a machine whose install cannot update itself needs a person; it holds no
// other machine of its platform.
func TestDriverInstallKindProblemsNeedAPerson(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	a := r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	a.prepFail = "not_self_managed"
	r.node(t, "n2", "bravo", "linux-amd64", "1.4.0")
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 60)
	ts := targetsByName(t, r.f.store, run.ID)
	// no failure of the release: done, the machine that needs a person named with its reason
	if ts["alpha"].State != TargetNeedsPerson || ts["bravo"].State != TargetHealthy || run.State != RunDoneNeedsPerson {
		t.Fatalf("run %s alpha %s bravo %s", run.State, ts["alpha"].State, ts["bravo"].State)
	}
	if r.f.notes.find(NoticeNeedsPerson) == nil {
		t.Fatalf("notices %v", r.f.notes.kinds())
	}
	n := r.f.notes.find(NoticeRolloutNeedsYou)
	if n == nil || n.p.Params["count"] != 1 || n.p.Params["machines"] != "alpha (not_self_managed)" ||
		r.f.notes.find(NoticeRolloutComplete) != nil {
		t.Fatalf("the rollout's notice: %+v (notices %v)", n, r.f.notes.kinds())
	}
	if !strings.Contains(string(run.Error), `"needs_person":1`) {
		t.Fatalf("the run's counts: %s", run.Error)
	}
	// no catch-up retries it every tick: it waits for a person (an admin's Retry)
	a.mu.Lock()
	a.st, a.prepFail = nil, ""
	a.mu.Unlock()
	_ = r.d.Step(ctx)
	if cu, _ := r.f.store.ActiveRun(ctx); cu != nil {
		t.Fatalf("a catch-up of a machine that needs a person: %+v", cu)
	}
}

// review A6: a machine busy with a start step (its Python environment) is not failed for
// the time it takes.
func TestDriverNodeStartStepHoldsTheTimeout(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	a := r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	a.pendingFor = 30 // 30 minutes of the rig's clock, past node_health_timeout_minutes (10)
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 80)
	if ts := targetsByName(t, r.f.store, run.ID); ts["alpha"].State != TargetHealthy || run.State != RunDone {
		t.Fatalf("run %s alpha %+v", run.State, ts["alpha"])
	}
}

// review A8: a release withdrawn while the home's app stages it ends the run
// (the order withdrawn); a catch-up never spreads a withdrawn release.
func TestDriverWithdrawnReleaseStops(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.asks, r.home.appSlow = true, true
	_, _ = r.f.svc.Tick(ctx)
	r.stepN(8)
	run := r.active(t)
	if err := r.f.store.SetWithdrawn(ctx, "1.5.0", WithdrawnGone); err != nil {
		t.Fatal(err)
	}
	_ = r.d.Step(ctx)
	got, _ := r.f.store.GetRun(ctx, run.ID)
	if got.State != RunCancelled || !strings.Contains(string(got.Error), "withdrawn") || r.home.state() != MAborted {
		t.Fatalf("run %s %s home %s", got.State, got.Error, r.home.state())
	}
	if _, err := r.d.Retry(ctx, run.ID, []string{"n1"}, "admin"); err == nil {
		t.Fatal("a retry of a withdrawn release")
	}
}

// review A9: viewers who come during the drain hold the home's restart; a machine whose
// streams are watched waits while the others go; streaming machines go last.
func TestDriverTVGatesTheRestarts(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	a := r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	b := r.node(t, "n2", "bravo", "linux-amd64", "1.4.0")
	if _, err := r.f.db.Exec(`INSERT INTO channel_runtime (channel_id, viewer_count, host_id, state) VALUES ('c1', 2, 'n1', 'live')`); err != nil {
		t.Fatal(err)
	}
	r.home.onApply = func() {
		if r.viewers > 0 {
			t.Error("the home restarted with viewers")
		}
	}
	_, _ = r.f.svc.Tick(ctx)
	r.stepN(3) // prepared, gates passed, draining
	r.viewers = 4
	r.stepN(5)
	if run := r.active(t); run.State != RunDraining || r.home.applies != 0 {
		t.Fatalf("run %s applies %d", run.State, r.home.applies)
	}
	r.viewers = 0
	r.stepN(30)
	run := r.active(t)
	ts := targetsByName(t, r.f.store, run.ID)
	if ts["alpha"].Ord <= ts["bravo"].Ord {
		t.Fatalf("the streaming machine is not last: alpha %d bravo %d", ts["alpha"].Ord, ts["bravo"].Ord)
	}
	if b.version != "1.5.0" || a.applies != 0 {
		t.Fatalf("bravo %s alpha applies %d", b.version, a.applies)
	}
	if _, err := r.f.db.Exec(`UPDATE channel_runtime SET viewer_count = 0`); err != nil {
		t.Fatal(err)
	}
	if got := r.drive(t, 30); got.State != RunDone || a.version != "1.5.0" {
		t.Fatalf("run %s alpha %s", got.State, a.version)
	}
}

// review A10: no update run starts while a switch of the home is under way.
func TestSchedulerWaitsForASwitchover(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.f.svc.Blockers = append(r.f.svc.Blockers, SwitchoverBlocker{DB: r.f.db})
	if _, err := r.f.db.Exec(`INSERT INTO cluster_switchovers (id, state) VALUES ('s1', 'fencing')`); err != nil {
		t.Fatal(err)
	}
	if d, _ := r.f.svc.Tick(ctx); d.Action != "blocked" || d.Reason != BlockSwitchover {
		t.Fatalf("tick %+v", d)
	}
	if _, err := r.f.db.Exec(`UPDATE cluster_switchovers SET state = 'done'`); err != nil {
		t.Fatal(err)
	}
	if d, _ := r.f.svc.Tick(ctx); d.Action != "started" {
		t.Fatalf("tick %+v", d)
	}
}

// review A-R5: a run that waits for its desktop apps is superseded only when the newer
// release can start: a blocker leaves it (and its orders) as they are.
func TestSupersedeOnlyWhenTheNewerRunStarts(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	a := r.node(t, "n1", "alpha", "darwin-arm64", "1.4.0")
	a.asks, a.appSlow = true, true
	_, _ = r.f.svc.Tick(ctx)
	r.stepN(40)
	old := r.active(t)
	seed(t, r.f.store, rel("1.6.0", func(x *Release) {
		x.Assets = []Asset{{Name: "release.json", URL: "https://github.test/release.json"}}
	}))
	r.f.svc.Blockers = append(r.f.svc.Blockers, BlockerFunc(func(context.Context) (string, error) {
		return BlockSwitchover, nil
	}))
	if d, _ := r.f.svc.Tick(ctx); d.Action != "blocked" {
		t.Fatalf("tick %+v", d)
	}
	if got, _ := r.f.store.GetRun(ctx, old.ID); got.FinishedAt != nil || a.state() != MOrdered {
		t.Fatalf("superseded under a blocker: run %s machine %s", got.State, a.state())
	}
}

// D1 (2026-10-27): a home's desktop app that staged its order only after an automatic
// run's window (and its overrun) is not installed outside it: the order is withdrawn
// (nothing drained, nothing installed) and the next window's run installs it.
func TestDriverHomeAppStagedPastTheWindowWaitsForTheNext(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.asks, r.home.appSlow = true, true
	n := r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	_, _ = r.f.svc.Tick(ctx)
	r.stepN(5)
	r.f.now = r.f.now.Add(7 * time.Hour) // 10:00, past 03:00-05:00 and its overrun
	r.home.stage()
	got := r.drive(t, 5)
	if got.State != RunFailed || readErr(got.Error).Code != "outside_window" || r.home.applies != 0 ||
		r.home.state() != MAborted || r.drainedMachines() != 0 {
		t.Fatalf("run %s %s home %s applies %d", got.State, got.Error, r.home.state(), r.home.applies)
	}
	r.f.now = r.f.now.Add(17 * time.Hour) // the next night
	if d, err := r.f.svc.Tick(ctx); err != nil || d.Action != "started" {
		t.Fatalf("tick: %+v %v", d, err)
	}
	if got := r.drive(t, 40); got.State != RunDone || r.home.applies != 1 || n.version != "1.5.0" {
		t.Fatalf("run %s home applies %d alpha %s", got.State, r.home.applies, n.version)
	}
}

// A machine that needs a person beside one whose update failed: the run ended with a
// failure, as before (done_with_failures, not done_needs_person).
func TestDriverFailureAndNeedsPersonIsFailures(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	a := r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	a.prepFail = "not_self_managed"
	b := r.node(t, "n2", "bravo", "darwin-arm64", "1.4.0")
	b.prepFail = "download_failed"
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 80)
	if run.State != RunDoneFailures {
		ts := targetsByName(t, r.f.store, run.ID)
		t.Fatalf("run %s alpha %s bravo %s", run.State, ts["alpha"].State, ts["bravo"].State)
	}
	if r.f.notes.find(NoticeRolloutNeedsYou) != nil {
		t.Fatalf("notices %v", r.f.notes.kinds())
	}
}

// The home's attempt of a run whose only machines left need a person: installed.
func TestRunAttemptNeedsPersonInstalled(t *testing.T) {
	t.Parallel()
	if a := runAttempt(Run{ID: "r1", State: RunDoneNeedsPerson}); a.Outcome != OutcomeInstalled {
		t.Fatalf("outcome %s", a.Outcome)
	}
	if troubled[RunDoneNeedsPerson] {
		t.Fatal("a run whose machines need a person is not trouble")
	}
}
