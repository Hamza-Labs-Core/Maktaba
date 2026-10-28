package updates

import (
	"context"
	"strings"
	"testing"
)

// Story 35.6: the home's new release moves the TV to its streaming binary before it
// commits; how the run ends when that fails.

// AC3, as the owner decided it (2026-10-28): a TV change whose swap failed, or that
// failed on the air after it switched, rolls the release back, blocks it and tells the
// admin (a check that fails before any swap holds the TV instead: driver_tvheld_test.go).
func TestDriverFailedTVBlocksTheRelease(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.outcome, r.home.rbReason = MRolledBack, CodeFailedTV
	r.home.rbErr = &MachineError{Step: "tv", Code: CodeFailedTV, Detail: "TV change #7: swap_failed"}
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 8)
	if run.State != RunRolledBack || !strings.Contains(string(run.Error), CodeFailedTV) ||
		!strings.Contains(string(run.Error), "swap_failed") {
		t.Fatalf("run %s %s", run.State, run.Error)
	}
	if rel, _ := r.f.store.Get(ctx, "1.5.0"); rel.BlockedReason != CodeFailedTV {
		t.Fatalf("blocked %q", rel.BlockedReason)
	}
	if r.f.notes.find(NoticeRolledBack) == nil {
		t.Fatalf("notices %v", r.f.notes.kinds())
	}
}

// AC4: queued behind a change awaiting approval until the window ended: a gate (as a
// watched TV), tried again in the next window; nothing counts against the release.
func TestDriverTVChangePendingIsAGate(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.outcome, r.home.rbReason = MRolledBack, CodeTVChangePending
	r.home.rbErr = &MachineError{Step: "tv", Code: CodeTVChangePending, Detail: "waited behind TV change #6"}
	_, _ = r.f.svc.Tick(ctx)
	run := r.drive(t, 8)
	if run.State != RunFailed || !strings.Contains(string(run.Error), CodeTVChangePending) {
		t.Fatalf("run %s %s", run.State, run.Error)
	}
	if rel, _ := r.f.store.Get(ctx, "1.5.0"); rel.BlockedReason != "" || rel.Attempts != 0 {
		t.Fatalf("a pending TV change counts against the release: %+v", rel)
	}
	// the automatic run kept to its window: the apply told the home when it ends
	body, _ := r.home.applyBody.(map[string]any)
	if body == nil || body["tv_deadline"] == nil {
		t.Fatalf("apply %v", r.home.applyBody)
	}
}

// A change that did not reach the air in time is an attempt like any failed run; an
// admin's cancel of it blocks the release until a retry.
func TestDriverTVTimeoutAndCancel(t *testing.T) {
	t.Parallel()
	r := newDriverRig(t)
	ctx := context.Background()
	r.home.outcome, r.home.rbReason = MRolledBack, CodeTVTimeout
	r.home.rbErr = &MachineError{Step: "tv", Code: CodeTVTimeout}
	_, _ = r.f.svc.Tick(ctx)
	if run := r.drive(t, 8); run.State != RunRolledBack {
		t.Fatalf("run %s %s", run.State, run.Error)
	}
	if rel, _ := r.f.store.Get(ctx, "1.5.0"); rel.BlockedReason != "" || rel.Attempts != 1 {
		t.Fatalf("a TV timeout: %+v", rel)
	}

	c := newDriverRig(t)
	c.home.outcome, c.home.rbReason = MRolledBack, CodeTVChangeCancelled
	c.home.rbErr = &MachineError{Step: "tv", Code: CodeTVChangeCancelled}
	_, _ = c.f.svc.Tick(ctx)
	if run := c.drive(t, 8); run.State != RunRolledBack {
		t.Fatalf("run %s %s", run.State, run.Error)
	}
	if rel, _ := c.f.store.Get(ctx, "1.5.0"); rel.BlockedReason != "tv_cancelled" {
		t.Fatalf("cancelled: %+v", rel)
	}
}
