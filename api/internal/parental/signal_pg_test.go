package parental_test

// The device's signal against a real migrated Postgres (story 57.04): the ratchet,
// the clear, the `ac` claim's source, and the limit a request is held to.

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

func np(n int) *int { return &n }

// familySeq numbers the sign-ins the package's tests make (they run side by side).
var familySeq atomic.Int64

// family gives user a live sign-in (a refresh-token family) and returns its id.
func (e *env) family(user string) string {
	e.t.Helper()
	fam := fmt.Sprintf("00000000-0000-4000-8000-0000574f%04d", familySeq.Add(1))
	e.exec(`INSERT INTO refresh_tokens (id, user_id, hash, family_id, expires_at)
	        VALUES (gen_random_uuid(), $1, $2, $3, now() + interval '30 days')`, user, "h-"+fam, fam)
	return fam
}

func (e *env) record(user, fam string, in parental.SignalInput) *parental.SignalState {
	e.t.Helper()
	if p := in.Normalize(); len(p) != 0 {
		e.t.Fatalf("signal %+v: %v", in, p)
	}
	st, err := e.store.RecordSignal(context.Background(), user, fam, in)
	if err != nil {
		e.t.Fatalf("record signal: %v", err)
	}
	return st
}

func (e *env) ceiling(user, fam string) *int {
	e.t.Helper()
	ac, err := e.store.AgeCeiling(context.Background(), user, fam)
	if err != nil {
		e.t.Fatalf("age ceiling: %v", err)
	}
	return ac
}

func same(a *int, want int, has bool) bool {
	if !has {
		return a == nil
	}
	return a != nil && *a == want
}

// T04: the strictest of the three is stored, with what the device said, and is
// what a refresh puts in `ac`.
func TestSignalStoresTheStrictestOfThree(t *testing.T) {
	t.Parallel()
	e := setup(t)
	fam := e.family(uViewer)
	st := e.record(uViewer, fam, parental.SignalInput{
		AgeRange:     &parental.AgeRange{Lower: np(13), Upper: np(15), Declaration: "guardian_declared"},
		MovieCeiling: np(400), TVCeiling: np(400), Region: "sa",
	})
	if !same(st.TierLimit, parental.Tier9, true) || st.Pending {
		t.Fatalf("state = %+v, want 9 applied", st)
	}
	var (
		lower, upper, movie, tv, limit int
		decl, region                   string
	)
	if err := e.db.QueryRow(`SELECT lower, upper, declaration, movie_ceiling, tv_ceiling, region, tier_limit
	                           FROM session_age_signals WHERE refresh_family_id = $1 AND user_id = $2`, fam, uViewer).
		Scan(&lower, &upper, &decl, &movie, &tv, &region, &limit); err != nil {
		t.Fatal(err)
	}
	if lower != 13 || upper != 15 || decl != "guardian_declared" || movie != 400 || tv != 400 || region != "SA" || limit != 9 {
		t.Fatalf("row = %d %d %q %d %d %q %d", lower, upper, decl, movie, tv, region, limit)
	}
	if ac := e.ceiling(uViewer, fam); !same(ac, 9, true) {
		t.Fatalf("ac = %v, want 9", ac)
	}
	// the applied signal is in the audit log, by the account itself
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2 AND actor_user_id = $3`,
		parental.AuditSignalApplied, uViewer, uViewer); n != 1 {
		t.Fatalf("applied audit rows = %d, want 1", n)
	}
	// a sign-in that never sent one has none
	if ac := e.ceiling(uViewer, e.family(uViewer)); ac != nil {
		t.Fatalf("ac of a silent sign-in = %v, want none", *ac)
	}
	// a device that limits nothing stores a signal with no limit, and no `ac`
	open := e.family(uViewer)
	st = e.record(uViewer, open, parental.SignalInput{AgeRange: &parental.AgeRange{Lower: np(18)}, MovieCeiling: np(1000), TVCeiling: np(1000)})
	if st.TierLimit != nil || e.ceiling(uViewer, open) != nil {
		t.Fatalf("an unlimited device: %+v", st)
	}
	if n := e.count(`SELECT count(*) FROM session_age_signals WHERE refresh_family_id = $1 AND tier_limit IS NULL`, open); n != 1 {
		t.Fatal("the unlimited signal was not stored")
	}
}

// T06 (store side) and T07: a stricter statement applies at once; a looser one
// waits and changes nothing; a clear applies what waited; a clear with nothing
// waiting removes the signal; a fresh sign-in starts with none.
func TestSignalRatchet(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := context.Background()
	e.restrict(uChild) // 9+
	fam := e.family(uChild)
	var pendings []string
	e.store.OnSignalPending = func(_ context.Context, user, family string) { pendings = append(pendings, user+"/"+family) }

	st := e.record(uChild, fam, parental.SignalInput{MovieCeiling: np(400)}) // R: 16
	if !same(st.TierLimit, 16, true) {
		t.Fatalf("first = %+v", st)
	}
	st = e.record(uChild, fam, parental.SignalInput{MovieCeiling: np(300)}) // PG-13: stricter
	if !same(st.TierLimit, 13, true) || st.Pending {
		t.Fatalf("stricter = %+v", st)
	}
	st = e.record(uChild, fam, parental.SignalInput{AgeRange: &parental.AgeRange{Lower: np(18)}, MovieCeiling: np(1000), Region: "US"})
	if !same(st.TierLimit, 13, true) || !st.Pending {
		t.Fatalf("looser = %+v, want 13 kept and pending", st)
	}
	if ac := e.ceiling(uChild, fam); !same(ac, 13, true) {
		t.Fatalf("ac after a looser statement = %v, want 13", ac)
	}
	// what was applied still describes the stricter statement
	if n := e.count(`SELECT count(*) FROM session_age_signals WHERE refresh_family_id = $1 AND movie_ceiling = 300
	                    AND tier_limit = 13 AND pending_looser->>'movie_ceiling' = '1000' AND pending_looser->'tier_limit' = 'null'::jsonb
	                    AND pending_looser->>'region' = 'US'`, fam); n != 1 {
		t.Fatal("the stored row is not the stricter statement with the looser one pending")
	}
	// the same looser statement again is not news; a different one is
	e.record(uChild, fam, parental.SignalInput{AgeRange: &parental.AgeRange{Lower: np(18)}, MovieCeiling: np(1000), Region: "US"})
	if len(pendings) != 1 || pendings[0] != uChild+"/"+fam {
		t.Fatalf("pending notices = %v, want one", pendings)
	}
	st = e.record(uChild, fam, parental.SignalInput{MovieCeiling: np(400)}) // 16: still looser than 13
	if !same(st.TierLimit, 13, true) || !st.Pending || len(pendings) != 2 {
		t.Fatalf("second looser = %+v, notices %v", st, pendings)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`, parental.AuditSignalPending, uChild); n != 2 {
		t.Fatalf("pending audit rows = %d, want 2", n)
	}

	// the guardian clears: what waited is applied, exactly
	cl, err := e.store.ClearSignal(ctx, uGuardian, uChild, fam)
	if err != nil || !same(cl.TierLimit, 16, true) || cl.Removed {
		t.Fatalf("clear = %+v, %v; want 16 applied", cl, err)
	}
	if n := e.count(`SELECT count(*) FROM session_age_signals WHERE refresh_family_id = $1 AND movie_ceiling = 400
	                    AND tier_limit = 16 AND pending_looser IS NULL`, fam); n != 1 {
		t.Fatal("the cleared row is not the statement that waited")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2 AND actor_user_id = $3`,
		parental.AuditSignalCleared, uChild, uGuardian); n != 1 {
		t.Fatalf("cleared audit rows = %d, want 1", n)
	}
	// an equal statement after a pending one drops the pending one
	e.record(uChild, fam, parental.SignalInput{MovieCeiling: np(1000)})
	st = e.record(uChild, fam, parental.SignalInput{TVCeiling: np(600)}) // TV-MA: 16, equal
	if !same(st.TierLimit, 16, true) || st.Pending {
		t.Fatalf("equal = %+v", st)
	}
	// nothing waiting: a clear removes the signal
	cl, err = e.store.ClearSignal(ctx, uAdmin, uChild, fam)
	if err != nil || !cl.Removed || cl.TierLimit != nil {
		t.Fatalf("clear with nothing waiting = %+v, %v", cl, err)
	}
	if e.ceiling(uChild, fam) != nil {
		t.Fatal("the removed signal still gives a limit")
	}
	// and there is nothing left to clear, nor on another account or a made-up sign-in
	for _, c := range [][2]string{{uChild, fam}, {uViewer, fam}, {uChild, "not-a-family"}, {"nobody", fam}} {
		if _, err := e.store.ClearSignal(ctx, uAdmin, c[0], c[1]); !errors.Is(err, parental.ErrSignalNotFound) {
			t.Fatalf("clear %v = %v, want ErrSignalNotFound", c, err)
		}
	}

	// T07: a fresh sign-in has no signal, and its first statement applies whole
	fresh := e.family(uChild)
	if e.ceiling(uChild, fresh) != nil {
		t.Fatal("a fresh sign-in has a limit")
	}
	if st := e.record(uChild, fresh, parental.SignalInput{}); st.TierLimit != nil || st.Pending {
		t.Fatalf("fresh, nothing said = %+v", st)
	}
}

// A signal is only ever stored on a live sign-in of the account that sent it, and
// the signals of ended sign-ins are swept.
func TestSignalNeedsALiveSignIn(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := context.Background()
	mine, theirs := e.family(uViewer), e.family(uChild)
	in := parental.SignalInput{MovieCeiling: np(300)}
	for name, c := range map[string][2]string{
		"another account's sign-in": {uViewer, theirs},
		"a made-up family":          {uViewer, "00000000-0000-4000-8000-00000000dead"},
		"no family":                 {uViewer, ""},
		"not a uuid":                {uViewer, "x"},
	} {
		if _, err := e.store.RecordSignal(ctx, c[0], c[1], in); !errors.Is(err, parental.ErrNoSignIn) {
			t.Errorf("%s: %v, want ErrNoSignIn", name, err)
		}
	}
	e.record(uViewer, mine, in)
	// the sign-in ends (signed out): its signal takes no more statements, and the
	// next statement from another sign-in sweeps it
	e.exec(`UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1`, mine)
	if _, err := e.store.RecordSignal(ctx, uViewer, mine, in); !errors.Is(err, parental.ErrNoSignIn) {
		t.Fatalf("a revoked sign-in: %v, want ErrNoSignIn", err)
	}
	next := e.family(uViewer)
	e.record(uViewer, next, in)
	if n := e.count(`SELECT count(*) FROM session_age_signals WHERE user_id = $1`, uViewer); n != 1 {
		t.Fatalf("signals of the viewer = %d, want only the live sign-in's", n)
	}
	if n := e.count(`SELECT count(*) FROM session_age_signals`); n != 1 {
		t.Fatalf("signals in all = %d, want 1 (nothing stored for a refused statement)", n)
	}
	// a rotated family is still the same sign-in
	e.exec(`UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1`, next)
	e.exec(`INSERT INTO refresh_tokens (id, user_id, hash, family_id, expires_at)
	        VALUES (gen_random_uuid(), $1, 'rotated', $2, now() + interval '30 days')`, uViewer, next)
	if st := e.record(uViewer, next, parental.SignalInput{MovieCeiling: np(100)}); !same(st.TierLimit, 0, true) {
		t.Fatalf("after a rotation = %+v", st)
	}
}

// T05 and the fold: a request is held to the stricter of its account's policy and
// its sign-in's signal; another sign-in of the same account is not; an account
// with no policy is held to the signal alone.
func TestEffectiveIsTheStricterOfPolicyAndSignal(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := context.Background()
	e.restrict(uChild) // 9+
	cache := parental.NewCache(e.store)
	read := func(user, fam string, ac *int) *parental.Effective {
		t.Helper()
		eff, err := cache.For(ctx, &principal.Principal{UserID: user, Source: principal.SourceJWT, RefreshFamily: fam, AgeCeiling: ac})
		if err != nil {
			t.Fatalf("cache read: %v", err)
		}
		return eff
	}
	now := time.Now()
	nine, thirteen, sixteen := 9, 13, 16

	// T05: an adult signal on a 9+ account leaves it at 9+; no signal, the same
	adult := e.family(uChild)
	e.record(uChild, adult, parental.SignalInput{AgeRange: &parental.AgeRange{Lower: np(18)}, MovieCeiling: np(1000), TVCeiling: np(1000)})
	for _, fam := range []string{adult, e.family(uChild), ""} {
		if eff := read(uChild, fam, nil); !eff.Restricted || eff.MaxTier != 9 || eff.SignalOnly || eff.SignalLimit != nil {
			t.Fatalf("9+ account, family %q: %+v", fam, eff)
		}
	}
	// a stricter device tightens that sign-in only
	strict := e.family(uChild)
	e.record(uChild, strict, parental.SignalInput{AgeRange: &parental.AgeRange{Lower: np(5), Upper: np(9)}})
	if eff := read(uChild, strict, nil); eff.MaxTier != 0 || eff.Permits(parental.Item{Tier: &nine}, now) {
		t.Fatalf("stricter device: %+v", eff)
	}
	if eff := read(uChild, adult, nil); eff.MaxTier != 9 {
		t.Fatalf("the other sign-in was tightened too: %+v", eff)
	}

	// an account with no policy: the signal alone, unrated hidden by default
	fam := e.family(uViewer)
	if eff := read(uViewer, fam, nil); eff.Restricted {
		t.Fatalf("no signal yet: %+v", eff)
	}
	e.record(uViewer, fam, parental.SignalInput{AgeRange: &parental.AgeRange{Lower: np(9), Upper: np(12)}})
	eff := read(uViewer, fam, nil)
	if !eff.Restricted || !eff.SignalOnly || eff.MaxTier != 9 || eff.Unrated != parental.UnratedHide {
		t.Fatalf("signal on an open account: %+v", eff)
	}
	if eff.Permits(parental.Item{Tier: &sixteen}, now) || !eff.Permits(parental.Item{Tier: &nine}, now) || eff.Permits(parental.Item{}, now) {
		t.Fatal("signal on an open account: 16+ shown, 9+ hidden or unrated shown")
	}
	if other := read(uViewer, e.family(uViewer), nil); other.Restricted {
		t.Fatalf("the open account's other sign-in is limited: %+v", other)
	}
	// the home's setting for unrated content on such a sign-in
	e.exec(`UPDATE app_settings SET value = '"tier_9"'::jsonb WHERE key = $1`, parental.SettingUnratedOnSignal)
	if eff := read(uViewer, fam, nil); eff.Unrated != "tier_9" || !eff.Permits(parental.Item{}, now) {
		t.Fatalf("unrated_on_signal tier_9: %+v", eff)
	}
	e.exec(`UPDATE app_settings SET value = '"everything"'::jsonb WHERE key = $1`, parental.SettingUnratedOnSignal)
	if eff := read(uViewer, fam, nil); eff.Unrated != parental.UnratedHide {
		t.Fatalf("a setting that is no rule: %+v", eff)
	}

	// a looser statement waits: the request is still held to the stricter one
	e.record(uViewer, fam, parental.SignalInput{MovieCeiling: np(400)})
	if eff := read(uViewer, fam, nil); eff.MaxTier != 9 || !eff.SignalPending {
		t.Fatalf("looser waiting: %+v", eff)
	}

	// the token's claim holds too: the stricter of it and the stored signal
	if eff := read(uViewer, fam, np(0)); eff.MaxTier != 0 {
		t.Fatalf("claim 0 over stored 9: %+v", eff)
	}
	if eff := read(uViewer, fam, np(16)); eff.MaxTier != 9 {
		t.Fatalf("claim 16 under stored 9: %+v", eff)
	}
	// a claim with no family, or on a sign-in whose signal is gone, still limits
	if eff := read(uViewer, "", np(13)); !eff.Restricted || eff.MaxTier != 13 || eff.Permits(parental.Item{Tier: &sixteen}, now) || !eff.Permits(parental.Item{Tier: &thirteen}, now) {
		t.Fatalf("claim alone: %+v", eff)
	}
	if eff := read(uChild, "", np(-1)); eff.MaxTier != parental.NothingAllowed {
		t.Fatalf("claim -1 on the 9+ account: %+v", eff)
	}

	// fail closed: the signal cannot be read, so the request is refused
	broken := parental.NewCache(parental.NewStore(e.db))
	e.exec(`ALTER TABLE session_age_signals RENAME TO session_age_signals_gone`)
	_, err := broken.For(ctx, &principal.Principal{UserID: uViewer, Source: principal.SourceJWT, RefreshFamily: fam})
	e.exec(`ALTER TABLE session_age_signals_gone RENAME TO session_age_signals`)
	if err == nil {
		t.Fatal("an unreadable signal gave a policy instead of an error")
	}
}

// A signal written or cleared by another process is seen on the next request: the
// listening cache drops the account's signals on the notification.
func TestCacheDropsASignalOnChange(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache := parental.NewCache(e.store)
	done := make(chan struct{})
	go func() { defer close(done); cache.Listen(ctx, e.dsn, nil, nil) }()
	for deadline := time.Now().Add(10 * time.Second); !cache.Listening(); {
		if time.Now().After(deadline) {
			t.Fatal("the cache never started listening")
		}
		time.Sleep(10 * time.Millisecond)
	}
	fam := e.family(uViewer)
	p := &principal.Principal{UserID: uViewer, Source: principal.SourceJWT, RefreshFamily: fam}
	until := func(what string, ok func(*parental.Effective) bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; {
			eff, err := cache.For(ctx, p)
			if err != nil {
				t.Fatalf("cache read: %v", err)
			}
			if ok(eff) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("five seconds on, %s: %+v", what, eff)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	until("an account with no signal is limited", func(eff *parental.Effective) bool { return !eff.Restricted })
	other := parental.NewStore(e.db) // "another process": no call into this cache
	if _, err := other.RecordSignal(ctx, uViewer, fam, parental.SignalInput{MovieCeiling: np(300)}); err != nil {
		t.Fatal(err)
	}
	until("the new signal does not hold", func(eff *parental.Effective) bool { return eff.Restricted && eff.MaxTier == 13 })
	if _, err := other.RecordSignal(ctx, uViewer, fam, parental.SignalInput{MovieCeiling: np(100)}); err != nil {
		t.Fatal(err)
	}
	until("the tightened signal does not hold", func(eff *parental.Effective) bool { return eff.MaxTier == 0 })
	if _, err := other.ClearSignal(ctx, uAdmin, uViewer, fam); err != nil {
		t.Fatal(err)
	}
	until("the cleared signal still holds", func(eff *parental.Effective) bool { return !eff.Restricted })
	// the setting's writer drops everything with an empty notification
	if _, err := other.RecordSignal(ctx, uViewer, fam, parental.SignalInput{MovieCeiling: np(300)}); err != nil {
		t.Fatal(err)
	}
	until("the signal does not hold", func(eff *parental.Effective) bool { return eff.Unrated == parental.UnratedHide && eff.MaxTier == 13 })
	e.exec(`UPDATE app_settings SET value = '"tier_13"'::jsonb WHERE key = $1`, parental.SettingUnratedOnSignal)
	e.exec(`SELECT pg_notify($1, '')`, parental.NotifyChannel)
	until("the changed setting is not read", func(eff *parental.Effective) bool { return eff.Unrated == "tier_13" })
	cancel()
	<-done
}
