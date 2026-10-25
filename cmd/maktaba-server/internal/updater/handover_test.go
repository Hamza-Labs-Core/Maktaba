package updater

import (
	"errors"
	"sync"
	"testing"
)

// Story 35.7: the prepared report says whether the machine's TV runs on across its
// restart (the home's driver then holds no TV gate), and the apply arms the handover
// right before its exit 75 onto the new release, saying in the run why not when it
// cannot.
func TestApplyArmsTheTVHandover(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	var mu sync.Mutex
	var calls []string
	r.m.TVHandover = func(_ *Journal, target string, arm bool) (bool, string) {
		mu.Lock()
		defer mu.Unlock()
		if arm {
			calls = append(calls, "arm "+target)
		} else {
			calls = append(calls, "check "+target)
		}
		return true, ""
	}
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	if j := r.m.Status().Journal; !j.HandsOver {
		t.Fatalf("the prepared report says no handover: %+v", j)
	}
	_ = r.m.Apply("run-1")
	r.waitExit()
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || calls[0] != "check 1.5.0" || calls[1] != "arm 1.5.0" {
		t.Fatalf("calls %v", calls)
	}
}

// One that cannot hand over says why, and every process restarts (AC4's degrade).
func TestApplyWithoutHandoverSaysWhy(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	r.m.TVHandover = func(*Journal, string, bool) (bool, string) {
		return false, "the launcher holds no handle to the TV processes' job"
	}
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	_ = r.m.Apply("run-1")
	r.waitExit()
	j := r.m.Status().Journal
	if j.HandsOver || j.HandoverWhy == "" {
		t.Fatalf("journal %+v", j)
	}
}

// Review L1: a run ordered while the TV is watched (the home skipped its TV gate for this
// machine's prepared hands_over) is refused when the handover cannot be made now, and
// the report says so; with the TV quiet it applies (every process restarts).
func TestApplyGatedRefusesALostHandover(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	can := true
	var mu sync.Mutex
	r.m.TVHandover = func(*Journal, string, bool) (bool, string) {
		mu.Lock()
		defer mu.Unlock()
		if can {
			return true, ""
		}
		return false, "a slot runs where it cannot outlive the supervisor"
	}
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	mu.Lock()
	can = false
	mu.Unlock()
	if err := r.m.ApplyGated("run-1", true); !errors.Is(err, ErrTVBusy) {
		t.Fatalf("a watched TV it can no longer hand over: %v", err)
	}
	if j := r.m.Status().Journal; j.State != StatePrepared || j.HandsOver {
		t.Fatalf("journal %+v", j)
	}
	if err := r.m.ApplyGated("run-1", false); err != nil {
		t.Fatal(err)
	}
	r.waitExit()
}
