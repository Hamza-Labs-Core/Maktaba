package updater

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/attempts"
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
	if j.Job.RunID != "run-2" || !w.waitCalled(t, "discard") || w.staged["1.5.0"] {
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
	if !w.waitCalled(t, "discard") || w.next != "1.4.0" || w.called(StepSwitch) {
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
	n.health.setMoving()
	n.waitState(StateWatching)
	// the fake clock moves a minute a read: far past the 3-minute health limit
	time.Sleep(10 * time.Millisecond)
	if j := n.m.Status().Journal; j.State != StateWatching || w.marked {
		t.Fatalf("while the database step works: %s marked %v", j.State, w.marked)
	}
	n.health.set(healthy)
	n.waitState(StateCommitted)
}

// review A-P: a start step that shows no progress for stallLimit (a dump that stopped
// growing, a migration that does not end) rolls back, and the dump is put back.
func TestWatchGivesUpOnAStalledStartStep(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	_ = r.m.Apply("run-1")
	r.waitExit()
	w.migrated = true
	n := r.restart(Verdict{Pending: true, Why: "api: Saving the library database (100 MB so far)", Progress: "100 MB"})
	n.waitExit()
	j := n.waitState(StateRollingBack)
	if j.Reason != "start_timeout" || !w.marked || w.next != "1.4.0" {
		t.Fatalf("reason %q marked %v next %s", j.Reason, w.marked, w.next)
	}
}

// review A-P: a start step that makes progress is still bounded (pendingCap: two hours
// plus the database's share), and the time it took is on the journal.
func TestWatchBoundsTheStartSteps(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	_ = r.m.Apply("run-1")
	r.waitExit()
	w.migrated = true
	n := r.restart(Verdict{Pending: true, Why: "api: Updating the library database"})
	n.health.setMoving()
	n.waitExit()
	j := n.waitState(StateRollingBack)
	if j.Reason != "start_timeout" || !w.marked {
		t.Fatalf("reason %q marked %v", j.Reason, w.marked)
	}
	if capped := pendingCap(j); time.Duration(j.PendingSec)*time.Second < capped-time.Minute {
		t.Fatalf("gave up after %d s, the cap is %s", j.PendingSec, capped)
	}
}

// review A-G1: the launcher's guard is kept ahead for the whole watch, not only while
// the start steps work: it catches a supervisor that died, not a slow commit.
func TestGuardKeptAheadThroughTheWatch(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	_ = r.m.Apply("run-1")
	r.waitExit()
	n := r.restart(healthy)
	n.waitState(StateCommitted)
	w.mu.Lock()
	pushed := w.extends
	w.mu.Unlock()
	// the 10-minute watch at a minute a check: about one push a minute, beside beginWatch's
	if pushed < 5 {
		t.Fatalf("the guard was pushed on %d times in a healthy 10-minute watch", pushed)
	}
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

// Review round 3: a start step whose progress cannot be seen (one long migration
// statement, an index built for more than an hour on a large home) is not stalled: only
// pendingCap bounds it.
func TestWatchLetsAnUnmeasuredStepRunPastTheStallLimit(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	// a large home: its cap is hours past the stall limit
	_ = r.m.mutate(func(j *Journal) { j.Rehearsal = &Rehearsal{Skipped: "large", DBBytes: 40 << 30} })
	_ = r.m.Apply("run-1")
	r.waitExit()
	w.migrated = true
	n := r.restart(Verdict{Pending: true, Unmeasured: true, Why: "api: Updating the library database (2 migrations done)",
		Progress: "2 migrations done"})
	n.health.turnAfter(150, healthy) // 150 checks: hours on the fake clock
	j := n.waitState(StateCommitted)
	if time.Duration(j.PendingSec)*time.Second < stallLimit(j) || j.Reason != "" || w.marked {
		t.Fatalf("pending %d s (stall limit %s), reason %q, marked %v", j.PendingSec, stallLimit(j), j.Reason, w.marked)
	}
	if want := int((pendingCap(j) + j.Job.WatchBound()).Seconds()); n.m.Status().BoundSec != want {
		t.Fatalf("bound %d, want %d", n.m.Status().BoundSec, want)
	}
}

// update-attempts.md: the machine's status carries its attempts (the desktop app's file
// and the server's), and a change of either calls OnChange (a node's agent sends them).
func TestStatusCarriesTheAttempts(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	if got := r.m.Status().Attempts; len(got) != 0 {
		t.Fatalf("none yet: %+v", got)
	}
	changed := make(chan struct{}, 1)
	r.m.OnChange = func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.m.WatchAttempts(ctx, 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	app := `{"attempts": [{"id": "app-1", "at": "2026-10-24T05:30:00Z", "source": "app", "to": "1.5.0",
	  "outcome": "failed", "step": "verify", "code": "signature_mismatch"}]}`
	if err := os.WriteFile(filepath.Join(r.dir, attempts.AppFile), []byte(app), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("no OnChange after the app wrote its attempts")
	}
	if got := r.m.Status().Attempts; len(got) != 1 || got[0].Code != "signature_mismatch" {
		t.Fatalf("status attempts %+v", got)
	}
}
