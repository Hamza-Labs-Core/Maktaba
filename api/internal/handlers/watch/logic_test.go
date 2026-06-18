package watch

import (
	"testing"
	"time"
)

func TestPercentComplete(t *testing.T) {
	cases := []struct {
		pos, dur, want float64
	}{
		{0, 600, 0},
		{300, 600, 50},
		{600, 600, 100},
		{700, 600, 100}, // clamp high
		{-5, 600, 0},    // clamp low
		{300, 0, 0},     // unknown duration
		{300, -1, 0},    // bad duration
	}
	for _, c := range cases {
		if got := PercentComplete(c.pos, c.dur); got != c.want {
			t.Errorf("PercentComplete(%v,%v)=%v want %v", c.pos, c.dur, got, c.want)
		}
	}
}

func TestStopState(t *testing.T) {
	if StopState(95) != StateCompleted {
		t.Error("95% should be completed")
	}
	if StopState(99.9) != StateCompleted {
		t.Error("99.9% should be completed")
	}
	if StopState(94.9) != StateStopped {
		t.Error("94.9% should be stopped")
	}
	if StopState(0) != StateStopped {
		t.Error("0% should be stopped")
	}
}

func TestCreditedSeconds(t *testing.T) {
	base := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	timeout := 5 * time.Minute

	// Normal 30s heartbeat gap → 30s credited.
	if got := CreditedSeconds(base, base.Add(30*time.Second), timeout); got != 30 {
		t.Errorf("30s gap credited %d want 30", got)
	}
	// Backwards clock → 0 (never negative).
	if got := CreditedSeconds(base, base.Add(-time.Minute), timeout); got != 0 {
		t.Errorf("backwards clock credited %d want 0", got)
	}
	// Equal timestamps → 0.
	if got := CreditedSeconds(base, base, timeout); got != 0 {
		t.Errorf("zero gap credited %d want 0", got)
	}
	// Huge gap (pause/abandon) → clamped to the stale timeout, not the
	// whole gap (D3).
	if got := CreditedSeconds(base, base.Add(2*time.Hour), timeout); got != 300 {
		t.Errorf("2h gap credited %d want 300 (clamped)", got)
	}
}

func TestIsStale(t *testing.T) {
	base := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	timeout := 5 * time.Minute

	// Exactly at the timeout is NOT yet stale (boundary exclusive).
	if IsStale(base, base.Add(timeout), timeout) {
		t.Error("exactly-at-timeout should not be stale")
	}
	// Just past is stale.
	if !IsStale(base, base.Add(timeout+time.Second), timeout) {
		t.Error("past timeout should be stale")
	}
	// Recent heartbeat is not stale.
	if IsStale(base, base.Add(30*time.Second), timeout) {
		t.Error("recent heartbeat should not be stale")
	}
}

// TestLifecycle walks start → heartbeat → heartbeat → stop as a pure
// sequence, asserting the credited/percent transitions the handler
// computes (Story 29.1 acceptance, DB-free per the repo convention).
func TestLifecycle(t *testing.T) {
	const durationSec = 600.0
	timeout := DefaultStaleTimeout
	t0 := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

	// start: duration 0, percent 0.
	watched := 0
	lastHB := t0

	// heartbeat #1 at +30s, position 30 → +30s, ~5%.
	hb1 := t0.Add(30 * time.Second)
	watched += CreditedSeconds(lastHB, hb1, timeout)
	lastHB = hb1
	if watched != 30 {
		t.Fatalf("after hb1 watched=%d want 30", watched)
	}
	if pct := PercentComplete(30, durationSec); pct < 4 || pct > 6 {
		t.Fatalf("after hb1 pct=%v want ~5", pct)
	}

	// heartbeat #2 at +300s total, position 300 → +270s, 50%.
	hb2 := t0.Add(300 * time.Second)
	watched += CreditedSeconds(lastHB, hb2, timeout)
	lastHB = hb2
	if watched != 300 {
		t.Fatalf("after hb2 watched=%d want 300", watched)
	}

	// stop at +580s, position 580 (≥95%) → completed.
	stopAt := t0.Add(580 * time.Second)
	watched += CreditedSeconds(lastHB, stopAt, timeout)
	pct := PercentComplete(580, durationSec)
	if got := StopState(pct); got != StateCompleted {
		t.Fatalf("stop state=%s want completed (pct=%v)", got, pct)
	}
	if watched <= 300 {
		t.Fatalf("final watched=%d should exceed 300", watched)
	}
}

func TestMergeActivity(t *testing.T) {
	t0 := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	watched := []ActivityItem{
		{Kind: "watched", At: t0.Add(10 * time.Minute)},
		{Kind: "watched", At: t0.Add(2 * time.Minute)},
	}
	searched := []ActivityItem{
		{Kind: "searched", At: t0.Add(5 * time.Minute)},
	}
	got := MergeActivity([][]ActivityItem{watched, searched}, 0, 10)
	if len(got) != 3 {
		t.Fatalf("merged len=%d want 3", len(got))
	}
	// Newest first.
	if !got[0].At.After(got[1].At) || !got[1].At.After(got[2].At) {
		t.Errorf("merge not sorted newest-first: %+v", got)
	}
	if got[0].Kind != "watched" || got[1].Kind != "searched" {
		t.Errorf("unexpected order: %s,%s", got[0].Kind, got[1].Kind)
	}

	// Pagination: offset past the end yields empty, not nil-panic.
	if out := MergeActivity([][]ActivityItem{watched}, 99, 10); len(out) != 0 {
		t.Errorf("offset past end len=%d want 0", len(out))
	}
	// Limit truncates.
	if out := MergeActivity([][]ActivityItem{watched, searched}, 0, 2); len(out) != 2 {
		t.Errorf("limit=2 len=%d want 2", len(out))
	}
}

func TestWantedKinds(t *testing.T) {
	if k := wantedKinds(nil); !k["watched"] || !k["searched"] || !k["rated"] {
		t.Error("empty types should select all kinds")
	}
	if k := wantedKinds([]string{"watched"}); !k["watched"] || k["searched"] {
		t.Error("explicit watched should exclude searched")
	}
	if k := wantedKinds([]string{"bogus"}); !k["watched"] {
		t.Error("all-invalid types should fall back to all kinds")
	}
}

func TestClampLimit(t *testing.T) {
	if clampLimit(0, 50, 200) != 50 {
		t.Error("zero → default")
	}
	if clampLimit(500, 50, 200) != 200 {
		t.Error("over-max → max")
	}
	if clampLimit(75, 50, 200) != 75 {
		t.Error("in-range → as-is")
	}
}
