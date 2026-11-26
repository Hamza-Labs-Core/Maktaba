package nodecopy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
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

// #1667: when the home pauses copies for migrations (copies_paused), the manager does not
// count login refusals or pause/back off the subscription; it re-checks every few seconds
// and resumes as soon as the pause ends. Older homes (copies_paused=false) keep existing
// refusal backoff.
func TestManagerCopiesPausedDoesNotBackOff(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	authErr := &pq.Error{Code: "28000", Message: "pg_hba.conf rejects connection for host"}

	// Grant JSON unmarshals copies_paused cleanly (and defaults false for older homes).
	var g Grant
	if err := json.Unmarshal([]byte(`{"role":"r","copies_paused":true}`), &g); err != nil || !g.CopiesPaused {
		t.Fatalf("grant with copies_paused=true: %+v %v", g, err)
	}
	var oldGrant Grant
	if err := json.Unmarshal([]byte(`{"role":"r"}`), &oldGrant); err != nil || oldGrant.CopiesPaused {
		t.Fatalf("older grant without copies_paused: %+v %v", oldGrant, err)
	}

	// Case 1: home says copies_paused=true -> no refusal counted, no Pause(), watch polls fast and exits once unpaused.
	pausedOnHome := true
	subPaused := 0
	checks := 0
	m := &Manager{
		Retry:           time.Minute,
		Every:           time.Hour,
		CopyLoginEvery:  time.Second,
		CopyAuthAfter:   2,
		CopyPausedEvery: 5 * time.Millisecond,
		Now:             func() time.Time { return now },
		observeFn:       func(context.Context, time.Time) Status { return Status{State: StateBehind, enabled: true} },
		receivingFn:     func(context.Context) (bool, error) { return false, nil },
		copyLoginFn:     func(context.Context, string) error { return authErr },
		pauseFn:         func(context.Context) error { subPaused++; return nil },
		copiesPausedFn: func(context.Context) bool {
			checks++
			return pausedOnHome
		},
	}
	m.homeRefusedLogin() // avoid background Report calls in unit test

	for i := 0; i < 5; i++ {
		now = now.Add(2 * time.Second)
		st := m.copyLogin(ctx, Status{State: StateBehind, enabled: true})
		if st.State == StateError || !strings.Contains(st.Detail, "paused") {
			t.Fatalf("iter %d: unexpected status while home paused: %+v", i, st)
		}
		if subPaused != 0 || m.copyAuth != 0 || m.copyPaused() {
			t.Fatalf("iter %d: home pause triggered refusal backoff (subPaused=%d copyAuth=%d)", i, subPaused, m.copyAuth)
		}
	}

	// watch() polls every CopyPausedEvery and returns promptly when home unpauses.
	checks = 0
	m.copiesPausedFn = func(context.Context) bool {
		checks++
		return checks < 3
	}
	done := make(chan bool, 1)
	go func() {
		done <- m.watch(ctx, time.Minute)
	}()
	select {
	case ok := <-done:
		if !ok || m.isHomePaused() || checks < 3 {
			t.Fatalf("watch after unpause: ok=%v homePaused=%v checks=%d", ok, m.isHomePaused(), checks)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not return promptly after home pause ended")
	}

	// Case 2: older home or genuine auth failure (copies_paused=false) keeps existing refusal backoff.
	subPaused = 0
	m2 := &Manager{
		Retry:          time.Minute,
		CopyLoginEvery: time.Second,
		CopyAuthAfter:  2,
		AuthBackoffMax: 30 * time.Minute,
		Now:            func() time.Time { return now },
		receivingFn:    func(context.Context) (bool, error) { return false, nil },
		copyLoginFn:    func(context.Context, string) error { return authErr },
		pauseFn:        func(context.Context) error { subPaused++; return nil },
		copiesPausedFn: func(context.Context) bool { return false },
	}
	now = now.Add(2 * time.Second)
	_ = m2.copyLogin(ctx, Status{State: StateBehind, enabled: true})
	now = now.Add(2 * time.Second)
	st := m2.copyLogin(ctx, Status{State: StateBehind, enabled: true})
	if subPaused != 1 || !m2.copyPaused() || st.State != StateError {
		t.Fatalf("genuine auth refusal should still pause and back off: subPaused=%d paused=%v status=%+v",
			subPaused, m2.copyPaused(), st)
	}
}
