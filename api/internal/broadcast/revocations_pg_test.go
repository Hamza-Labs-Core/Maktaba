package broadcast

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Story 50.25 on the home, against the database: an off_air entry for a published channel stops
// it (state revoked, the reason kept) and a later list lifts it; T04: with the copy expired two
// days ago the published channel keeps running, a warning shows, and a new publish is refused
// until the list is refreshed; a restart keeps the copy and its anti-rollback.
func TestTheHomeHonoursTheRevocationList(t *testing.T) {
	db, ch, admin := homeWithChannel(t)
	ctx := context.Background()
	s := newRevSigner("cloud-1")
	now := time.Now().UTC().Truncate(time.Second)
	src := &fakeRevSource{text: s.sign(1, now)}
	cloud := &fakeCloud{plan: broadcasterPlan}
	store := &Store{DB: db}
	rev := &Revocations{Store: store, Source: src, Keys: s.keys(), Now: func() time.Time { return now }}
	svc := &Service{Store: store, Cloud: cloud, Revocations: rev, Now: func() time.Time { return now }, QuietHour: 99}
	if _, err := svc.SaveSetup(ctx, ch, SetupRequest{Slug: "seerah"}); err != nil {
		t.Fatal(err)
	}
	_ = store.AddUplink(ctx, 100_000, "publish")
	got, err := svc.Publish(ctx, ch, true, Admin{UserID: admin, Name: "Amina"})
	if err != nil || got.State != StateLive {
		t.Fatalf("publish with a fresh list: %+v %v", got, err)
	}

	// a service admin takes the channel off air: the next poll stops it and keeps the reason
	src.set(s.sign(2, now, RevEntry{ID: "r-1", Kind: "channel", Ref: got.EdgeChannel, Action: "off_air", Reason: "notice"}), nil)
	now = now.Add(RevocationsPoll)
	svc.Tick(ctx)
	c, _ := store.Get(ctx, ch)
	if c.State != StateRevoked || c.Detail != "list:notice" {
		t.Fatalf("after the off_air entry: %s %q", c.State, c.Detail)
	}
	// a republish is refused while the entry holds
	if _, err := svc.Unpublish(ctx, ch); err != nil {
		t.Fatal(err)
	}
	var p *Problem
	if _, err := svc.Publish(ctx, ch, true, Admin{UserID: admin, Name: "Amina"}); !errors.As(err, &p) || p.Refusal.Code != "revoked.channel" ||
		p.Refusal.Params["reason"] != "notice" {
		t.Fatalf("a republish under the entry: %v", err)
	}
	// lifted: the channel publishes again
	src.set(s.sign(3, now), nil)
	now = now.Add(RevocationsPoll)
	svc.Tick(ctx)
	if got, err = svc.Publish(ctx, ch, true, Admin{UserID: admin, Name: "Amina"}); err != nil || got.State != StateLive {
		t.Fatalf("after the lift: %+v %v", got, err)
	}
	// a list put on by the list is lifted by the list: revoke then lift while published
	src.set(s.sign(4, now, RevEntry{ID: "r-2", Kind: "global", Action: "off_air", Reason: "incident"}), nil)
	now = now.Add(RevocationsPoll)
	svc.Tick(ctx)
	if c, _ := store.Get(ctx, ch); c.State != StateRevoked || c.Detail != "list:incident" {
		t.Fatalf("after a global entry: %s %q", c.State, c.Detail)
	}
	src.set(s.sign(5, now), nil)
	now = now.Add(RevocationsPoll)
	svc.Tick(ctx)
	if c, _ := store.Get(ctx, ch); c.State != StateLive {
		t.Fatalf("after the global entry was lifted: %s %q", c.State, c.Detail)
	}

	// T04: the cloud goes away; four days on, the copy (48 h) expired two days ago
	src.set("", ErrUnreachable)
	now = now.Add(96 * time.Hour)
	svc.Tick(ctx)
	if c, _ := store.Get(ctx, ch); c.State != StateLive {
		t.Fatalf("a published channel with an expired copy: %s %q", c.State, c.Detail)
	}
	st := rev.Status(ctx, "", "")
	if !st.Stale || st.Days != 4 {
		t.Fatalf("the warning: %+v", st)
	}
	if _, err := svc.Unpublish(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Publish(ctx, ch, true, Admin{UserID: admin, Name: "Amina"}); !errors.As(err, &p) || p.Refusal.Code != "revocationsStale" ||
		p.Refusal.Params["days"] != 4 {
		t.Fatalf("a new publish with an expired copy: %v", err)
	}
	// refreshed: the list no longer holds the publish back (four days on, the test's 24 h
	// schedule has run out, so the publishable-hours check is what answers now)
	src.set(s.sign(5, now), nil)
	if _, err := svc.Publish(ctx, ch, true, Admin{UserID: admin, Name: "Amina"}); errors.As(err, &p) && p.Refusal.Code == "revocationsStale" {
		t.Fatalf("after the refresh: %v", err)
	}
	if st := rev.Status(ctx, "", ""); st.Stale {
		t.Fatalf("still stale after the refresh: %+v", st)
	}

	// a restart: the saved copy is enforced and an older list is refused
	again := &Revocations{Store: store, Source: src, Keys: s.keys(), Now: func() time.Time { return now }}
	if l := again.Current(ctx); l == nil || l.Version != 5 {
		t.Fatalf("after a restart: %+v", l)
	}
	if err := again.Accept(ctx, s.sign(4, now)); !errors.Is(err, ErrRevocationOlder) {
		t.Fatalf("an older list after a restart: %v", err)
	}
}
