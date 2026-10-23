package updater

import (
	"testing"
	"time"
)

// askRig is a machine whose install asks first (a desktop app that runs the server,
// owner decision D1), prepared for run-1.
func askRig(t *testing.T, w *world) *rig {
	t.Helper()
	r := startRig(t, t.TempDir(), w, healthy)
	r.inst.asks = true
	if err := r.m.Prepare(homeJob()); err != nil {
		t.Fatal(err)
	}
	r.waitState(StatePrepared)
	return r
}

// restartAsking is restart (healthy) on an install that asks.
func (r *rig) restartAsking() *rig {
	r.cancel()
	// this process is gone before the next reads the journal (its wait may be saving it)
	done := make(chan struct{})
	go func() { r.m.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	n := startRig(r.t, r.dir, r.w, healthy)
	n.inst.asks = true
	n.m.Resume()
	return n
}

// waitOrder waits for the app's order of run (written by the machine's wait).
func (r *rig) waitOrder(run string) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.w.mu.Lock()
		o := r.w.order
		r.w.mu.Unlock()
		if o == run {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	r.t.Fatalf("no order of %s", run)
}

func (r *rig) noExit() {
	r.t.Helper()
	select {
	case code := <-r.exited:
		r.t.Fatalf("exit %d before the go", code)
	default:
	}
}

// TC (D1): an install that asks is ordered from the app at apply; it switches only once
// someone clicked there and the home said go.
func TestAskFirstWaitsForTheClickAndTheGo(t *testing.T) {
	w := newWorld("1.4.0")
	r := askRig(t, w)
	if st := r.m.Status(); !st.Asks {
		t.Fatal("status does not say the machine asks")
	}
	if err := r.m.Apply("run-1"); err != nil {
		t.Fatal(err)
	}
	r.waitState(StateAwaitingClick)
	r.waitOrder("run-1")
	w.app(RequestStaged)
	deadline := time.Now().Add(5 * time.Second)
	for r.m.Status().App != RequestStaged && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	// asked again before the click: still waiting
	if err := r.m.Apply("run-1"); err != nil {
		t.Fatal(err)
	}
	if j := r.m.Status().Journal; j.State != StateAwaitingClick || j.App != RequestStaged {
		t.Fatalf("before the click: %s app %q", j.State, j.App)
	}
	w.app(RequestClicked)
	r.waitState(StateClicked)
	r.noExit()
	if w.called(StepSwitch) || w.next != "1.4.0" {
		t.Fatalf("switched before the go: calls %v", w.calls)
	}
	if err := r.m.Apply("run-1"); err != nil { // the home's go
		t.Fatal(err)
	}
	r.waitExit()
	n := r.restart(healthy)
	j := n.waitState(StateCommitted)
	ask := false
	for _, s := range j.Steps {
		if s.Step == StepAsk && s.State == "done" && s.Detail == "clicked" {
			ask = true
		}
	}
	if !ask || w.committed != "1.5.0" {
		t.Fatalf("steps %+v committed %q", j.Steps, w.committed)
	}
}

// TC (D1): a machine that stops while its order waits takes it up where it was: the
// order still waits for its click, a click it got still waits for the go.
func TestAskFirstResumesWhereItWaited(t *testing.T) {
	w := newWorld("1.4.0")
	r := askRig(t, w)
	_ = r.m.Apply("run-1")
	r.waitState(StateAwaitingClick)
	r.waitOrder("run-1")
	n := r.restartAsking()
	n.waitState(StateAwaitingClick)
	w.app(RequestClicked) // clicked while the machine was down
	n = n.restartAsking()
	n.waitState(StateClicked)
	n2 := n.restartAsking()
	n2.waitState(StateClicked)
	n2.noExit()
	if err := n2.m.Apply("run-1"); err != nil {
		t.Fatal(err)
	}
	n2.waitExit()
	n2.restart(healthy).waitState(StateCommitted)
}

// TC (D1): an order is taken back before its switch (Abort, or a newer run in its
// place), and one the app cannot take fails with nothing changed.
func TestAskFirstAbortWithdrawsTheOrder(t *testing.T) {
	w := newWorld("1.4.0")
	r := askRig(t, w)
	_ = r.m.Apply("run-1")
	r.waitState(StateAwaitingClick)
	r.waitOrder("run-1")
	w.app(RequestClicked)
	r.waitState(StateClicked)
	if err := r.m.Abort("run-1"); err != nil {
		t.Fatal(err)
	}
	r.waitState(StateAborted)
	if w.order != "" || w.staged["1.5.0"] || w.called(StepSwitch) {
		t.Fatalf("order %q staged %v calls %v", w.order, w.staged, w.calls)
	}
}

func TestAskFirstNewerRunReplacesTheOrder(t *testing.T) {
	w := newWorld("1.4.0")
	r := askRig(t, w)
	_ = r.m.Apply("run-1")
	r.waitState(StateAwaitingClick)
	next := homeJob()
	next.RunID, next.Version, next.Tag = "run-2", "1.6.0", "v1.6.0"
	if err := r.m.Prepare(next); err != nil {
		t.Fatalf("a newer run while the order waits: %v", err)
	}
	j := r.waitState(StatePrepared)
	if j.Job.RunID != "run-2" || w.staged["1.5.0"] || !w.called("discard") {
		t.Fatalf("run %s staged %v", j.Job.RunID, w.staged)
	}
	_ = r.m.Apply("run-2")
	r.waitState(StateAwaitingClick)
	r.waitOrder("run-2")
}

func TestAskFirstAppFailureChangesNothing(t *testing.T) {
	w := newWorld("1.4.0")
	r := askRig(t, w)
	w.answerErr = Fail("app_install_failed", "no space to stage")
	_ = r.m.Apply("run-1")
	j := r.waitState(StateApplyFailed)
	if j.Error == nil || j.Error.Code != "app_install_failed" || j.Error.Step != StepAsk {
		t.Fatalf("error %+v", j.Error)
	}
	if w.next != "1.4.0" || w.called(StepSwitch) || !w.called("discard") {
		t.Fatalf("next %s calls %v", w.next, w.calls)
	}
}

// review 1: a release busy starting (the home's database step dumping and migrating for
// longer than the health limit) is neither rolled back nor committed meanwhile.
func TestWatchWaitsForTheStartSteps(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	_ = r.m.Apply("run-1")
	r.waitExit()
	w.migrated = true
	n := r.restart(Verdict{Pending: true, Why: "api: Updating the library database"})
	n.waitState(StateWatching)
	// the fake clock moves a minute a read: far past the 3-minute health limit
	time.Sleep(50 * time.Millisecond)
	if j := n.m.Status().Journal; j.State != StateWatching || w.marked {
		t.Fatalf("while the database step works: %s marked %v", j.State, w.marked)
	}
	n.health.set(healthy)
	n.waitState(StateCommitted)
}

// review 5: a commit that cannot clear the launcher's guard is not a commit: the watch
// goes on and commits once it can.
func TestCommitRetriedUntilItHolds(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	_ = r.m.Apply("run-1")
	r.waitExit()
	n := startRig(t, r.dir, w, healthy)
	n.inst.failAt, n.inst.failTimes = StepCommit, 3
	r.cancel()
	n.m.Resume()
	n.waitState(StateCommitted)
	if n.inst.failed < 4 || w.committed != "1.5.0" {
		t.Fatalf("commit tries %d committed %q", n.inst.failed, w.committed)
	}
}

// review 3: a switch back that fails leaves the new release running on its own
// database: the restore mark is taken back, so no later start puts the dump back.
func TestRollbackThatCannotSwitchBackKeepsTheDatabase(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	_ = r.m.Apply("run-1")
	r.waitExit()
	w.migrated = true
	n := startRig(t, r.dir, w, Verdict{Fatal: true, Why: "api crash loop"})
	n.inst.failAt = "revert"
	r.cancel()
	n.m.Resume()
	j := n.waitState(StateFailed)
	if w.marked || j.Error == nil {
		t.Fatalf("marked %v error %+v", w.marked, j.Error)
	}
	select {
	case <-n.exited:
		t.Fatal("exit after a failed switch back")
	default:
	}
}
