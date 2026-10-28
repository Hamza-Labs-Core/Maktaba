package updater

import (
	"context"
	"errors"
	"strings"
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

// Binary implements TVBinary.
func (f *fakeTV) Binary(j *Journal) (string, string, error) { return "v" + j.Job.Version, "ab12", nil }

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

// TC3 (story 35.6): a TV change that goes on the air within the watch is asked for once,
// and the release commits with it live.
func TestTVStepLiveWithinTheWatch(t *testing.T) {
	tv := &fakeTV{script: []TVStatus{{Outcome: TVQueued, ChangeID: "c1", Number: 7},
		{Outcome: TVInProgress, ChangeID: "c1", Number: 7, State: "verifying"},
		{Outcome: TVLive, ChangeID: "c1", Number: 7, State: "live"}}}
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

// The outage of 2026-10-28: the home passed every check, but its TV change (#57) sat in
// ready waiting for an admin's approval for half an hour; the approval's swap stopped a
// TV slot and a transcription worker failed its probes once, and the watch, whose bound
// had run on while the TV waited, rolled the release back (watch_timeout). The watch
// judges the home alone: once the home passed it, the release commits with the TV pending
// (the change not taken back, it goes on by itself), whatever comes after.
func TestTVWaitingForAnApprovalNeverTimesTheWatchOut(t *testing.T) {
	waiting := TVStatus{Outcome: TVInProgress, ChangeID: "c1", Number: 57, State: "ready", LiveStreaming: "v1.4.0"}
	tv := &fakeTV{script: []TVStatus{waiting}}
	n, w := appliedRig(t, tv, nil, func(n *rig) {
		// the probe that failed once, at the approval's swap (on main: past the watch,
		// within its bound, so its healthy run began again and the bound ran out)
		n.health.at = map[int]Verdict{7: {Why: "pipeline/stt-1 /healthz: connection refused"}}
	})
	j := n.waitState(StateCommitted)
	if w.committed != "1.5.0" || j.Error != nil || j.Reason != "" {
		t.Fatalf("committed %q error %+v reason %q", w.committed, j.Error, j.Reason)
	}
	select {
	case code := <-n.exited:
		t.Fatalf("exited %d: a rollback", code)
	default:
	}
	if begins, withdrawn := tv.took(); begins != 1 || len(withdrawn) != 0 {
		t.Fatalf("begins %d, the waiting change taken back: %v", begins, withdrawn)
	}
	if j.TV == nil || j.TV.Outcome != TVPending || j.TV.LiveStreaming != "v1.4.0" {
		t.Fatalf("tv %+v", j.TV)
	}
	s := stepOf(j, StepTV)
	if s == nil || s.State != TVPending || !strings.Contains(s.Detail, "#57") || !strings.Contains(s.Detail, "approval") {
		t.Fatalf("tv step %+v", s)
	}
	if got := j.TV.keeps(); len(got) != 1 || got[0] != "v1.4.0" {
		t.Fatalf("keeps %v: the release the live TV runs must stay", got)
	}
}

// A TV change that only waits never rolls the release back: queued behind a change that
// waits for an approval past the window's end, staged longer than its limit, or an API
// that does not answer about it. Each commits with the TV pending once the home passed
// its watch.
func TestTVStepWaitingCommitsPending(t *testing.T) {
	staged := time.Date(2026, 10, 22, 3, 0, 0, 0, time.UTC)
	deadline := time.Date(2026, 10, 22, 3, 5, 0, 0, time.UTC) // the window ends early in the watch
	for name, c := range map[string]struct {
		tv       *fakeTV
		deadline *time.Time
		says     string
	}{
		"approval past the window": {tv: &fakeTV{script: []TVStatus{{Outcome: TVQueued, ChangeID: "c1", Number: 8,
			QueuedBehind: &TVQueueRef{ID: "c0", Number: 7, State: "ready"}}}}, deadline: &deadline,
			says: CodeTVChangePending},
		"slow staging": {tv: &fakeTV{script: []TVStatus{{Outcome: TVInProgress, ChangeID: "c1", Number: 7,
			State: "swapping", StagedAt: &staged, TimeoutMinutes: 10}}}, says: CodeTVTimeout},
	} {
		t.Run(name, func(t *testing.T) {
			n, w := appliedRig(t, c.tv, c.deadline)
			j := n.waitState(StateCommitted)
			if w.committed != "1.5.0" || j.Error != nil {
				t.Fatalf("committed %q error %+v", w.committed, j.Error)
			}
			if _, withdrawn := c.tv.took(); len(withdrawn) != 0 {
				t.Fatalf("withdrawn %v", withdrawn)
			}
			s := stepOf(j, StepTV)
			if j.TV == nil || j.TV.Outcome != TVPending || s == nil || s.State != TVPending ||
				!strings.Contains(s.Detail, c.says) {
				t.Fatalf("tv %+v step %+v", j.TV, s)
			}
		})
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

// TC3, as the owner decided it (2026-10-28): a swap that fails, and a TV that fails on
// the air after it switched (Epic 33.9 rolled it back), roll the release back (failed_tv)
// and take the change back; the release that comes back says rolled_back.
func TestTVStepFailedTVRollsBack(t *testing.T) {
	for _, reason := range []string{"swap_failed", "rolled_back"} {
		t.Run(reason, func(t *testing.T) {
			tv := &fakeTV{script: []TVStatus{{Outcome: TVInProgress, ChangeID: "c1", Number: 7, State: "swapping"},
				{Outcome: TVFailed, ChangeID: "c1", Number: 7, Reason: reason}}}
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
		})
	}
}

// The owner's decision (2026-10-28): a check that fails before any swap holds the TV on
// the streaming software it runs, and the release commits all the same: no rollback, the
// change not taken back, the TV step held with the checks that failed, and the release the
// live TV runs recorded for the commit's pruning to keep.
func TestTVStepHeldCommits(t *testing.T) {
	tv := &fakeTV{script: []TVStatus{{Outcome: TVInProgress, ChangeID: "c1", Number: 7, State: "verifying",
		LiveStreaming: "v1.4.0"},
		{Outcome: TVHeld, ChangeID: "c1", Number: 7, State: "draft", Reason: "verify_failed",
			Failed: []string{"stream_plays"}, LiveStreaming: "v1.4.0"}}}
	n, w := appliedRig(t, tv, nil)
	j := n.waitState(StateCommitted)
	if w.committed != "1.5.0" || j.Error != nil || j.Reason != "" {
		t.Fatalf("committed %q error %+v reason %q", w.committed, j.Error, j.Reason)
	}
	if _, withdrawn := tv.took(); len(withdrawn) != 0 {
		t.Fatalf("the held change was taken back: %v", withdrawn)
	}
	if j.TV == nil || j.TV.Outcome != TVHeld || j.TV.LiveStreaming != "v1.4.0" ||
		len(j.TV.Failed) != 1 || j.TV.Failed[0] != "stream_plays" {
		t.Fatalf("tv %+v", j.TV)
	}
	s := stepOf(j, StepTV)
	if s == nil || s.State != "held" || !strings.Contains(s.Detail, "stream_plays") || !strings.Contains(s.Detail, "v1.4.0") {
		t.Fatalf("tv step %+v", s)
	}
	if got := j.TV.keeps(); len(got) != 1 || got[0] != "v1.4.0" {
		t.Fatalf("keeps %v", got)
	}
}

// The update to 0.3.39 (2026-10-28): the live slot, started unbound by the app's restart,
// ran 0.3.39's binary already when the check failed. The step says so rather than "the TV
// stays on" the release it installs.
func TestTVStepHeldOnThisReleaseSaysSo(t *testing.T) {
	tv := &fakeTV{script: []TVStatus{{Outcome: TVHeld, ChangeID: "c1", Number: 54, State: "draft",
		Reason: "verify_failed", Failed: []string{"ts.conformity"}, LiveStreaming: "v1.5.0"}}}
	n, w := appliedRig(t, tv, nil)
	j := n.waitState(StateCommitted)
	if w.committed != "1.5.0" || j.TV == nil || j.TV.Outcome != TVHeld {
		t.Fatalf("committed %q tv %+v", w.committed, j.TV)
	}
	s := stepOf(j, StepTV)
	if s == nil || s.State != "held" || strings.Contains(s.Detail, "stays on") ||
		!strings.Contains(s.Detail, "the live TV runs v1.5.0's streaming software already") {
		t.Fatalf("tv step %+v", s)
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

// The review of #433: an API that never answered about the change leaves no change behind
// the TV step to go on by itself. The release commits with the TV held (not pending), the
// check named no_change, and the binary the change carries recorded for the Updates page's
// Retry to ask for it.
func TestTVStepNoChangeIsHeldNotPending(t *testing.T) {
	tv := &fakeTV{err: errors.New("connection refused")}
	n, w := appliedRig(t, tv, nil)
	j := n.waitState(StateCommitted)
	if w.committed != "1.5.0" || j.Error != nil {
		t.Fatalf("committed %q error %+v", w.committed, j.Error)
	}
	if j.TV == nil || j.TV.Outcome != TVHeld || j.TV.ChangeID != "" || len(j.TV.Failed) != 1 ||
		j.TV.Failed[0] != NoTVChange || j.TV.StreamingVersion != "v1.5.0" || j.TV.StreamingSHA256 != "ab12" {
		t.Fatalf("tv %+v", j.TV)
	}
	if s := stepOf(j, StepTV); s == nil || s.State != "held" || !strings.Contains(s.Detail, "connection refused") {
		t.Fatalf("tv step %+v", s)
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
		if got := FrontDoorRestarts(c.release, c.running); got != c.restarts {
			t.Errorf("release %d running %d: restarts %v, want %v", c.release, c.running, got, c.restarts)
		}
	}
}
