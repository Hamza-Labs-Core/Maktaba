package nodecopy

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A refused login waits Retry, twice as long each time, up to AuthBackoffMax, and holds
// the reports back meanwhile; a slot about to be lost is said on the status; a rebuild's
// first copy is said as the rebuild it is, until it follows.
func TestManagerSlotAndLogins(t *testing.T) {
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	m := &Manager{Retry: time.Second, AuthBackoffMax: 10 * time.Second, Now: func() time.Time { return now }}
	for n, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 4: 8 * time.Second, 9: 10 * time.Second} {
		if got := m.authBackoff(n); got != want {
			t.Fatalf("authBackoff(%d) = %s, want %s", n, got, want)
		}
	}
	if m.homeLoginHeld() {
		t.Fatal("held before any refusal")
	}
	if d := m.homeRefusedLogin(); d != time.Second || !m.homeLoginHeld() {
		t.Fatalf("after a refusal: wait %s, held %v", d, m.homeLoginHeld())
	}
	if d := m.homeRefusedLogin(); d != 2*time.Second {
		t.Fatalf("after two refusals: wait %s", d)
	}
	m.homeLoginOK()
	if m.homeLoginHeld() {
		t.Fatal("held after the home let it in")
	}

	m.homeRefusedLogin() // holds the reports back: no home to tell in this test
	m.setSlotWAL(SlotUnreserved)
	m.set(context.Background(), Status{State: StateBehind})
	if s := m.Status(); s.Slot != SlotUnreserved || !strings.Contains(s.Detail, "about to give up") {
		t.Fatalf("a slot about to be lost: %+v", s)
	}
	m.setSlotWAL("reserved")
	m.set(context.Background(), Status{State: StateFollowing})
	if s := m.Status(); s.Slot != "" || s.Detail != "" {
		t.Fatalf("a slot that keeps what the copy needs: %+v", s)
	}

	m.rebuilding = "the home's slot for this copy lost the changes"
	if s := m.asRebuild(Status{State: StateCopying, Detail: "3 of 9 tables copied"}); s.State != StateRebuilding ||
		s.Detail != "the home's slot for this copy lost the changes: 3 of 9 tables copied" {
		t.Fatalf("a rebuild's first copy: %+v", s)
	}
	if s := m.asRebuild(Status{State: StateFollowing}); s.State != StateFollowing || m.rebuilding != "" {
		t.Fatalf("a rebuild that follows: %+v (%q)", s, m.rebuilding)
	}
}
