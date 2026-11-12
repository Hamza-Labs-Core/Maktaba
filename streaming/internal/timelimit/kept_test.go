package timelimit

import (
	"testing"
	"time"
)

func regAt(now *time.Time) *Registry {
	g := New()
	g.Now = func() time.Time { return *now }
	return g
}

// Lifting a restriction (SetKept false) drops everything held for the account, in
// every library: a library it was once refused in no longer refuses its new URLs
// (tk "0", no session), and a session that was running plays on.
func TestSetKeptFalseClearsTheAccount(t *testing.T) {
	now := time.Date(2026, 11, 10, 17, 0, 0, 0, time.UTC)
	g := regAt(&now)
	past, lease := now.Add(-time.Minute), now.Add(time.Minute)
	g.Set("child", "lib-a", &Limit{EndAt: &past, Reason: ReasonLimitReached})
	g.Set("child", "lib-b", &Limit{SessionID: "s1", LeaseUntil: &lease})
	g.Set("other", "lib-a", &Limit{EndAt: &past, Reason: ReasonLimitReached})
	if _, ref := g.Check("child", []string{"lib-a"}, ""); ref == nil {
		t.Fatal("an account with an entry was served a URL that names no session")
	}

	g.SetKept("child", false)
	for _, lib := range []string{"lib-a", "lib-b", "lib-c"} {
		if _, ref := g.Check("child", []string{lib}, ""); ref != nil {
			t.Fatalf("%s: a lifted account's new URL is refused: %+v", lib, ref)
		}
	}
	// the session that was running is served on, past its old lease, with no call
	now = now.Add(10 * time.Minute)
	if _, ref := g.Check("child", []string{"lib-b"}, "s1"); ref != nil {
		t.Fatalf("the running session of a lifted account was cut: %+v", ref)
	}
	// another account's entry is untouched
	if _, ref := g.Check("other", []string{"lib-a"}, ""); ref == nil {
		t.Fatalf("another account was cleared too: %+v", ref)
	}
}

// Making an account restricted (SetKept true) refuses the URLs signed before it,
// which name no session, in every library, also one nothing else is held for; and a
// session left free by an earlier lift needs a counted call again.
func TestSetKeptTrueRefusesEarlierURLs(t *testing.T) {
	now := time.Date(2026, 11, 10, 17, 0, 0, 0, time.UTC)
	g := regAt(&now)
	if _, ref := g.Check("child", []string{"lib-a"}, ""); ref != nil {
		t.Fatalf("an account nothing is held for was refused: %+v", ref)
	}
	lease := now.Add(time.Minute)
	g.Set("child", "lib-a", &Limit{SessionID: "s1", LeaseUntil: &lease})
	g.SetKept("child", false) // lifted: s1 plays free

	g.SetKept("child", true)
	if _, ref := g.Check("child", []string{"lib-z"}, ""); ref == nil || ref.Reason != ReasonNoHeartbeat {
		t.Fatalf("a URL signed before the account was restricted is served: %+v", ref)
	}
	if _, ref := g.Check("child", []string{"lib-a"}, "s1"); ref == nil || ref.Reason != ReasonNoHeartbeat {
		t.Fatalf("a session left free by a lift is served without a counted call: %+v", ref)
	}
	// a counted call gives it a lease again
	lease = now.Add(time.Minute)
	g.Set("child", "lib-a", &Limit{SessionID: "s1", LeaseUntil: &lease})
	if _, ref := g.Check("child", []string{"lib-a"}, "s1"); ref != nil {
		t.Fatalf("a renewed lease is refused: %+v", ref)
	}
	// saying it again changes nothing for a session under a real lease
	g.SetKept("child", true)
	if _, ref := g.Check("child", []string{"lib-a"}, "s1"); ref != nil {
		t.Fatalf("SetKept true cut a counted session: %+v", ref)
	}
	if _, ref := g.Check("other", []string{"lib-z"}, ""); ref != nil {
		t.Fatalf("another account was made kept: %+v", ref)
	}
}

// ClearEnds drops the account's end in every library and leaves it kept: its
// sessions still need their leases.
func TestClearEnds(t *testing.T) {
	now := time.Date(2026, 11, 10, 17, 0, 0, 0, time.UTC)
	g := regAt(&now)
	past, lease := now.Add(-time.Minute), now.Add(time.Minute)
	g.SetKept("child", true)
	g.Set("child", "lib-a", &Limit{EndAt: &past, Reason: ReasonOutsideSlot, SessionID: "s1", LeaseUntil: &lease})
	g.Set("child", "lib-b", &Limit{EndAt: &past, Reason: ReasonLimitReached})
	if _, ref := g.Check("child", []string{"lib-a"}, "s1"); ref == nil || ref.Reason != ReasonOutsideSlot {
		t.Fatalf("before: %+v", ref)
	}
	g.ClearEnds("child")
	if _, ref := g.Check("child", []string{"lib-a", "lib-b"}, "s1"); ref != nil {
		t.Fatalf("an end survived: %+v", ref)
	}
	if _, ref := g.Check("child", []string{"lib-b"}, ""); ref == nil {
		t.Fatal("the account stopped being kept")
	}
	now = now.Add(2 * time.Minute)
	if _, ref := g.Check("child", []string{"lib-a"}, "s1"); ref == nil || ref.Reason != ReasonNoHeartbeat {
		t.Fatalf("the lease no longer runs out: %+v", ref)
	}
}
