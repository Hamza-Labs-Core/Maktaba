package supervisor

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Epic 53 story 53.05 T02b: in safe mode the reconciler closes every component but the
// safe set, core.* included, reports each off with boot_flag {"flag": "--safe-mode"}, and
// writes no desired state.
func TestReconcileSafeMode(t *testing.T) {
	cat := components.Default()
	tab := &fakeTable{}
	tab.add("core.api", "api", false)
	tab.add("core.pipeline", "pipeline", false)
	tab.add("transcription", "stt-0", false)
	tab.add("core.streaming", "streaming", false)
	st := &fakeStore{}
	r, _ := newTestReconciler(cat, tab, st)
	r.SafeMode = true
	desired := map[string]store.Desired{"transcription": want("transcription", components.WantEnabled, 4)}
	passWith(r, desired)
	calls := tab.takeCalls()
	for _, inst := range []string{"pipeline", "stt-0", "streaming"} {
		if !slices.Contains(calls, inst+"=false") {
			t.Errorf("%s not closed in safe mode: %v", inst, calls)
		}
	}
	if slices.Contains(calls, "api=false") {
		t.Errorf("the api runs in safe mode: %v", calls)
	}
	ph := r.Phases()
	for _, id := range []string{"core.pipeline", "transcription", "core.streaming"} {
		if ph[id].Phase != components.PhaseOff || ph[id].Reason != ReasonBootFlag {
			t.Errorf("%s: %+v", id, ph[id])
		}
	}
	st.mu.Lock()
	rows := st.rows
	st.mu.Unlock()
	for _, row := range rows {
		if row.ID != "transcription" {
			continue
		}
		var found bool
		for _, c := range row.Conditions {
			if c.Reason == ReasonBootFlag && c.Params["flag"] == components.SafeModeFlag {
				found = true
			}
		}
		if !found {
			t.Errorf("transcription's row lacks boot_flag --safe-mode: %+v", row)
		}
	}
	if desired["transcription"].Generation != 4 || desired["transcription"].Want != components.WantEnabled {
		t.Error("safe mode must not change the desired state")
	}
}

// T02: the flag lasts one start: read once, then gone.
func TestSafeModeFlagLastsOneStart(t *testing.T) {
	dir := t.TempDir()
	if TakeSafeMode(dir) {
		t.Fatal("no flag, no safe mode")
	}
	if err := RequestSafeMode(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !TakeSafeMode(dir) {
		t.Fatal("the flag asks for safe mode")
	}
	if TakeSafeMode(dir) {
		t.Fatal("the next start is normal")
	}
	_ = RequestSafeMode(dir, time.Now())
	if err := CancelSafeMode(dir); err != nil || TakeSafeMode(dir) {
		t.Fatalf("cancel: %v", err)
	}
	if err := CancelSafeMode(dir); err != nil {
		t.Fatalf("cancel with no flag: %v", err)
	}
}

// Epic 53 story 53.05 T01: a crash loop whose slow retries fail too is cut off: it stays
// down (no next restart), a cut_off event says so, and a restart by request starts it.
func TestCrashLoopCutOff(t *testing.T) {
	p, _ := fakeProc(t, RoleAPI, "crash")
	pol := testPolicy()
	pol.MaxRestarts, pol.FailedRetry, pol.FailedRetries = 2, 30*time.Millisecond, 2
	s := New([]Process{p}, pol, WithLog(&strings.Builder{}))
	_, _ = running(t, s)
	snap := waitFor(t, s, "the cut-off", func(sn Snapshot) bool { return sn.Processes[0].CutOff })
	st := snap.Processes[0]
	if st.State != StateFailed || st.NextRestartAt != nil {
		t.Fatalf("cut off: %+v", st)
	}
	if !slices.ContainsFunc(snap.Events, func(e Event) bool { return e.Kind == EventCutOff }) {
		t.Fatal("no cut_off event")
	}
	n := st.Restarts
	time.Sleep(200 * time.Millisecond)
	if got := s.Snapshot().Processes[0].Restarts; got != n {
		t.Fatalf("a cut-off child must not restart by itself: %d → %d", n, got)
	}
	if err := s.Restart(RoleAPI, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "a restart by request", func(sn Snapshot) bool { return sn.Processes[0].Restarts > n })
}
