package updater

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeTV is the API's change controller as the TV step sees it: a script of answers, the
// last one repeated.
type fakeTV struct {
	mu        sync.Mutex
	script    []TVStatus
	n         int
	begins    int
	withdrawn []string
	err       error
}

func (f *fakeTV) next() (TVStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return TVStatus{}, f.err
	}
	st := f.script[min(f.n, len(f.script)-1)]
	f.n++
	return st, nil
}

func (f *fakeTV) Begin(context.Context, *Journal) (TVStatus, error) {
	f.mu.Lock()
	f.begins++
	f.mu.Unlock()
	return f.next()
}

func (f *fakeTV) Status(context.Context, *Journal) (TVStatus, error) { return f.next() }

func (f *fakeTV) Withdraw(_ context.Context, _ *Journal, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.withdrawn = append(f.withdrawn, reason)
	return nil
}

func (f *fakeTV) took() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.begins, append([]string(nil), f.withdrawn...)
}

// appliedRig is a home whose release 1.5.0 was prepared, applied and restarted on: the
// new release's process, its TV step over tv.
func appliedRig(t *testing.T, tv TVSwapper, deadline *time.Time, before ...func(*rig)) (*rig, *world) {
	t.Helper()
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	if err := r.m.Prepare(homeJob()); err != nil {
		t.Fatal(err)
	}
	r.waitState(StatePrepared)
	if deadline != nil {
		r.m.SetTVDeadline("run-1", *deadline)
	}
	if err := r.m.Apply("run-1"); err != nil {
		t.Fatal(err)
	}
	r.waitExit()
	r.waitState(StateRestarting)
	r.cancel()
	n := startRig(t, r.dir, w, healthy)
	n.m.TV = tv
	for _, f := range before {
		f(n)
	}
	n.m.Resume()
	return n, w
}

func stepOf(j *Journal, step string) *StepRecord {
	for i := len(j.Steps) - 1; i >= 0; i-- {
		if j.Steps[i].Step == step {
			return &j.Steps[i]
		}
	}
	return nil
}

// TC3 (story 35.6): the release commits only once its TV change is on the air, asked for
// once; the watch's own bound does not cut the wait short.
func TestTVStepWaitsForLive(t *testing.T) {
	tv := &fakeTV{script: []TVStatus{{Outcome: TVQueued, ChangeID: "c1", Number: 7}}}
	for i := 0; i < 40; i++ { // longer than the watch's bound (3 × 10 + 3 minutes)
		tv.script = append(tv.script, TVStatus{Outcome: TVInProgress, ChangeID: "c1", Number: 7, State: "verifying"})
	}
	tv.script = append(tv.script, TVStatus{Outcome: TVLive, ChangeID: "c1", Number: 7, State: "live"})
	n, w := appliedRig(t, tv, nil)
	j := n.waitState(StateCommitted)
	if w.committed != "1.5.0" {
		t.Fatalf("committed %q", w.committed)
	}
	if begins, withdrawn := tv.took(); begins != 1 || len(withdrawn) != 0 {
		t.Fatalf("begins %d withdrawn %v", begins, withdrawn)
	}
	if j.TV == nil || j.TV.Outcome != TVLive || j.TV.ChangeID != "c1" {
		t.Fatalf("tv %+v", j.TV)
	}
	if s := stepOf(j, StepTV); s == nil || s.State != "done" {
		t.Fatalf("tv step %+v", s)
	}
}

// Nothing to swap (staging off, the binary live already): the release commits.
func TestTVStepSkippedCommits(t *testing.T) {
	tv := &fakeTV{script: []TVStatus{{Outcome: TVSkipped, Skipped: "staging_off"}}}
	n, w := appliedRig(t, tv, nil)
	j := n.waitState(StateCommitted)
	if w.committed != "1.5.0" || stepOf(j, StepTV) == nil || stepOf(j, StepTV).State != "skipped" {
		t.Fatalf("committed %q, steps %+v", w.committed, j.Steps)
	}
}

// TC3: a failed verification rolls the release back (failed_tv, the checks named) and
// takes the change back; the release that comes back says rolled_back.
func TestTVStepFailedTVRollsBack(t *testing.T) {
	tv := &fakeTV{script: []TVStatus{{Outcome: TVInProgress, ChangeID: "c1", Number: 7},
		{Outcome: TVFailed, ChangeID: "c1", Number: 7, Reason: "verify_failed", Failed: []string{"stream_plays"}}}}
	n, w := appliedRig(t, tv, nil)
	n.waitExit()
	j := n.waitState(StateRollingBack)
	if j.Reason != CodeFailedTV || j.Error == nil || j.Error.Code != CodeFailedTV || j.Error.Step != StepTV {
		t.Fatalf("reason %q error %+v", j.Reason, j.Error)
	}
	if _, withdrawn := tv.took(); len(withdrawn) != 1 || withdrawn[0] != CodeFailedTV {
		t.Fatalf("withdrawn %v", withdrawn)
	}
	back := n.restart(healthy)
	j = back.waitState(StateRolledBack)
	if j.Error == nil || j.Error.Code != CodeFailedTV || w.committed != "" {
		t.Fatalf("rolled back: error %+v committed %q", j.Error, w.committed)
	}
}

// TC4: queued behind a change waiting for an approval past the window's end:
// tv_change_pending (the driver retries it next window).
func TestTVStepPendingApprovalTimesOut(t *testing.T) {
	tv := &fakeTV{script: []TVStatus{{Outcome: TVQueued, ChangeID: "c1", Number: 8,
		QueuedBehind: &TVQueueRef{ID: "c0", Number: 7, State: "ready"}}}}
	// the clock moves a minute a read: the window ends a few minutes into the watch
	deadline := time.Date(2026, 10, 22, 3, 20, 0, 0, time.UTC)
	n, _ := appliedRig(t, tv, &deadline)
	n.waitExit()
	j := n.waitState(StateRollingBack)
	if j.Error == nil || j.Error.Code != CodeTVChangePending {
		t.Fatalf("error %+v", j.Error)
	}
}

// A change staged but not on the air within boundary_max_wait + 10 minutes: tv_timeout.
func TestTVStepTimesOutInFlight(t *testing.T) {
	staged := time.Date(2026, 10, 22, 3, 0, 0, 0, time.UTC)
	tv := &fakeTV{script: []TVStatus{{Outcome: TVInProgress, ChangeID: "c1", Number: 7, State: "swapping",
		StagedAt: &staged, TimeoutMinutes: 40}}}
	n, _ := appliedRig(t, tv, nil)
	n.waitExit()
	j := n.waitState(StateRollingBack)
	if j.Error == nil || j.Error.Code != CodeTVTimeout {
		t.Fatalf("error %+v", j.Error)
	}
}

// An API that never answers about the change: tv_timeout after tvAskLimit, not a hang.
func TestTVStepGivesUpOnASilentAPI(t *testing.T) {
	tv := &fakeTV{err: errors.New("connection refused")}
	n, _ := appliedRig(t, tv, nil)
	n.waitExit()
	if j := n.waitState(StateRollingBack); j.Error == nil || j.Error.Code != CodeTVTimeout {
		t.Fatalf("error %+v", j.Error)
	}
}

// AC8: a release rolled back for another reason after its TV change went on the air
// takes the change back (its TV rolled back), once.
func TestRollbackTakesTheTVChangeBack(t *testing.T) {
	tv := &fakeTV{script: []TVStatus{{Outcome: TVLive, ChangeID: "c1", Number: 7}}}
	// the release cannot be kept yet (the commit fails, tried again): it is still watched
	// when it fails
	n, _ := appliedRig(t, tv, nil, func(n *rig) { n.inst.failAt = StepCommit })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if j := n.m.Status().Journal; j != nil && j.TV != nil && j.TV.Outcome == TVLive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the TV change never went live")
		}
		time.Sleep(time.Millisecond)
	}
	n.health.set(Verdict{Fatal: true, Why: "the api answers as 1.4.0"})
	n.waitExit()
	j := n.waitState(StateRollingBack)
	if _, withdrawn := tv.took(); len(withdrawn) != 1 || withdrawn[0] != j.Reason {
		t.Fatalf("withdrawn %v (reason %q)", withdrawn, j.Reason)
	}
	if j.TV == nil || j.TV.Withdrawn == "" {
		t.Fatalf("tv %+v", j.TV)
	}
}

// TC6: the front door restarts only when the release's edge protocol differs from the
// running front door's (or either does not say).
func TestEdgeProtocolGate(t *testing.T) {
	for _, c := range []struct {
		release, running int
		restarts         bool
	}{
		{1, 1, false},
		{2, 1, true},
		{0, 1, true},
		{1, 0, true},
	} {
		if got := FrontDoorRestarts(&Journal{EdgeProtocol: c.release}, c.running); got != c.restarts {
			t.Errorf("release %d running %d: restarts %v, want %v", c.release, c.running, got, c.restarts)
		}
	}
}
