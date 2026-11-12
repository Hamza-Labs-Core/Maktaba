package parental_test

// Epic 57 story 57.12: turning parental controls on in a home that already exists,
// against a real migrated Postgres (MAKTABA_TEST_PG_URL).

import (
	"context"
	"errors"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// off puts the home back as an update leaves it: nothing set up, the switch off.
func (e *env) off() {
	e.t.Helper()
	e.exec(`DELETE FROM parental_policies`)
	e.exec(`UPDATE libraries SET age_tier = NULL`)
	e.exec(`UPDATE app_settings SET value = 'false' WHERE key = 'parental.enabled'`)
}

func (e *env) enabled() bool {
	e.t.Helper()
	on, err := e.store.Enabled(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return on
}

// T02: while parental controls are off a device's signal is recorded, mints no `ac`,
// limits no request, and the admins are told once; turned on, what was recorded holds.
func TestSignalWhileOffIsRecordedNotApplied(t *testing.T) {
	e := setup(t)
	e.off()
	ctx := context.Background()
	var told []string
	e.store.OnChildDevice = func(_ context.Context, user, family string) { told = append(told, user+"/"+family) }
	pendings := 0
	e.store.OnSignalPending = func(context.Context, string, string) { pendings++ }

	fam := e.family(uViewer)
	child := parental.SignalInput{AgeRange: &parental.AgeRange{Lower: np(9), Upper: np(12)}}
	st := e.record(uViewer, fam, child)
	if !st.Off || st.TierLimit != nil || st.Pending {
		t.Fatalf("state while off = %+v, want off with no limit", st)
	}
	var stored int
	if err := e.db.QueryRow(`SELECT tier_limit FROM session_age_signals WHERE refresh_family_id = $1`, fam).Scan(&stored); err != nil || stored != parental.Tier9 {
		t.Fatalf("recorded limit = %d (%v), want 9", stored, err)
	}
	if ac := e.ceiling(uViewer, fam); ac != nil {
		t.Fatalf("ac while off = %d, want none", *ac)
	}
	cache := parental.NewCache(e.store)
	p := &principal.Principal{UserID: uViewer, RefreshFamily: fam, Source: principal.SourceJWT}
	eff, err := cache.For(ctx, p)
	if err != nil || eff.Restricted || eff.SignalLimit != nil || eff.SignalPending {
		t.Fatalf("effective while off = %+v (%v), want nothing limited", eff, err)
	}
	// a token minted before the switch went off limits nothing either
	withClaim := *p
	withClaim.AgeCeiling = np(parental.Tier9)
	if eff, err = cache.For(ctx, &withClaim); err != nil || eff.Restricted {
		t.Fatalf("effective with an old claim while off = %+v (%v)", eff, err)
	}
	if len(told) != 1 || told[0] != uViewer+"/"+fam {
		t.Fatalf("admins told %v, want once for the sign-in", told)
	}
	// an adult's device is no child's device: no notice
	adult := e.family(uGuardian)
	e.record(uGuardian, adult, parental.SignalInput{AgeRange: &parental.AgeRange{Lower: np(18)}})
	// a looser statement while off waits like any other and tells no guardian yet
	e.record(uViewer, fam, parental.SignalInput{AgeRange: &parental.AgeRange{Lower: np(18)}})
	if len(told) != 1 || pendings != 0 {
		t.Fatalf("told %v, pendings %d after an adult's device and a looser statement", told, pendings)
	}

	set, err := e.store.Settings(ctx)
	if err != nil || set.Enabled || set.ChildDevices != 1 || !set.ChildDeviceNotice || !set.CanDisable || set.RestrictedAccounts != 0 {
		t.Fatalf("settings while off = %+v (%v)", set, err)
	}
	if err := e.store.DismissNotice(ctx, uAdmin); err != nil {
		t.Fatal(err)
	}
	if set, err = e.store.Settings(ctx); err != nil || set.ChildDeviceNotice || set.ChildDevices != 1 {
		t.Fatalf("settings after the dismissal = %+v (%v), want no notice", set, err)
	}
	// another child's device after the dismissal shows it again
	fam2 := e.family(uOtherG)
	e.record(uOtherG, fam2, child)
	if set, err = e.store.Settings(ctx); err != nil || !set.ChildDeviceNotice || set.ChildDevices != 2 {
		t.Fatalf("settings after a second device = %+v (%v), want the notice again", set, err)
	}

	// turned on: the recorded limit is in force from the next request and the next token
	if err := e.store.SetEnabled(ctx, uAdmin, true); err != nil {
		t.Fatal(err)
	}
	cache.Invalidate("")
	if ac := e.ceiling(uViewer, fam); !same(ac, parental.Tier9, true) {
		t.Fatalf("ac once on = %v, want 9", ac)
	}
	if eff, err = cache.For(ctx, p); err != nil || !eff.Restricted || !eff.SignalOnly || eff.MaxTier != parental.Tier9 || !eff.SignalPending {
		t.Fatalf("effective once on = %+v (%v), want the device's 9+ with the looser one waiting", eff, err)
	}
	if set, err = e.store.Settings(ctx); err != nil || !set.Enabled || set.ChildDeviceNotice || set.ChildDevices != 0 {
		t.Fatalf("settings once on = %+v (%v)", set, err)
	}
}

// T03: the first restricted account turns parental controls on; off is refused while
// an account is restricted; each change of the switch is one audit row.
func TestFirstRestrictedAccountTurnsItOn(t *testing.T) {
	e := setup(t)
	e.off()
	ctx := context.Background()
	if e.enabled() {
		t.Fatal("on after off()")
	}
	audits := func() int {
		var n int
		if err := e.db.QueryRow(`SELECT count(*) FROM audit_log WHERE action IN ('parental.enabled', 'parental.disabled')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := audits()
	if _, err := e.store.SetPolicy(ctx, uAdmin, uChild, parental.PolicyInput{MaxTier: parental.Tier9, Unrated: parental.UnratedHide}); err != nil {
		t.Fatal(err)
	}
	if !e.enabled() || audits() != before+1 {
		t.Fatalf("after the first restricted account: enabled %v, %d audit rows", e.enabled(), audits()-before)
	}
	var by string
	if err := e.db.QueryRow(`SELECT updated_by::text FROM app_settings WHERE key = 'parental.enabled'`).Scan(&by); err != nil || by != uAdmin {
		t.Fatalf("updated_by = %q (%v)", by, err)
	}
	// a second account changes nothing about the switch
	if _, err := e.store.SetPolicy(ctx, uAdmin, uChild2, parental.PolicyInput{MaxTier: parental.Tier13, Unrated: parental.UnratedHide}); err != nil {
		t.Fatal(err)
	}
	if audits() != before+1 {
		t.Fatalf("a second account wrote %d switch rows", audits()-before)
	}
	if err := e.store.SetEnabled(ctx, uAdmin, false); !errors.Is(err, parental.ErrRestrictionsExist) {
		t.Fatalf("off with restricted accounts = %v, want ErrRestrictionsExist", err)
	}
	if !e.enabled() {
		t.Fatal("a refused off turned it off")
	}
	for _, c := range []string{uChild, uChild2} {
		if err := e.store.Lift(ctx, uAdmin, c); err != nil {
			t.Fatal(err)
		}
	}
	if !e.enabled() {
		t.Fatal("lifting the last restriction turned it off by itself")
	}
	if err := e.store.SetEnabled(ctx, uAdmin, false); err != nil {
		t.Fatal(err)
	}
	if e.enabled() || audits() != before+2 {
		t.Fatalf("after off: enabled %v, %d audit rows", e.enabled(), audits()-before)
	}
	// the admin token (no user row) can turn it on
	if err := e.store.SetEnabled(ctx, "admin-token", true); err != nil || !e.enabled() {
		t.Fatalf("on by the admin token: %v", err)
	}
	// a restricted account's policy is applied whatever the switch says
	if _, err := e.db.Exec(`UPDATE app_settings SET value = 'false' WHERE key = 'parental.enabled'`); err != nil {
		t.Fatal(err)
	}
	e.exec(`INSERT INTO parental_policies (user_id, max_tier, version) VALUES ($1, 9, 1)`, uChild)
	eff, err := parental.NewCache(e.store).For(ctx, &principal.Principal{UserID: uChild, Source: principal.SourceJWT})
	if err != nil || !eff.Restricted || eff.MaxTier != parental.Tier9 {
		t.Fatalf("a policy with the switch off = %+v (%v), want it applied", eff, err)
	}
	// a missing key counts as on
	e.exec(`DELETE FROM app_settings WHERE key = 'parental.enabled'`)
	if !e.enabled() {
		t.Fatal("a missing key reads as off")
	}
}

// T04 against the database: a library that is mostly lectures starts at All ages.
func TestLibrarySuggestions(t *testing.T) {
	e := setup(t)
	video := func(lib, typ string, n int) {
		for i := 0; i < n; i++ {
			e.exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, content_type)
				VALUES (gen_random_uuid(), $1::uuid, '/m/' || gen_random_uuid()::text, 'f.mp4', encode(sha256(gen_random_uuid()::text::bytea), 'hex'),
				        1, now(), 'ready', 't', NULLIF($2, ''))`, lib, typ)
		}
	}
	video(libOpen, "lecture", 17)
	video(libOpen, "sermon", 2)
	video(libOpen, "film", 1)
	video(libOpen, "unknown", 30)
	video(libOpen, "", 5)
	video(libClosed, "lecture", 5)
	video(libClosed, "film", 5)
	e.exec(`UPDATE libraries SET age_tier = 13 WHERE id = $1`, libClosed)
	got, err := e.store.LibrarySuggestions(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("suggestions = %+v (%v)", got, err)
	}
	by := map[string]parental.LibrarySuggestion{}
	for _, s := range got {
		by[s.LibraryID] = s
	}
	if s := by[libOpen]; s.Classified != 20 || s.Lectures != 19 || !same(s.SuggestedTier, 0, true) || s.Tier != nil {
		t.Fatalf("the lectures library = %+v, want All ages suggested from 19 of 20", s)
	}
	if s := by[libClosed]; s.Classified != 10 || s.Lectures != 5 || s.SuggestedTier != nil || !same(s.Tier, 13, true) {
		t.Fatalf("the mixed library = %+v, want nothing suggested and its 13+ kept", s)
	}
}

// T01 and T05's database step: slot 0225 on a populated home. A home with nothing set
// up goes off; one with a restricted account or a library default tier stays on;
// nothing else changes; down puts the seed back.
func TestSlot0225UpAndDownOnPostgres(t *testing.T) {
	up, down := gooseStatements(t, "0225_parental_enabled.sql")
	state := func(t *testing.T, e *env) string {
		t.Helper()
		var v string
		if err := e.db.QueryRow(`SELECT value::text FROM app_settings WHERE key = 'parental.enabled'`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	populated := func(t *testing.T) *env {
		e := setup(t) // users, libraries, an ACL
		run(t, e.db, "0225 down", down)
		if got := state(t, e); got != "true" {
			t.Fatalf("after down: %s, want slot 0221's true", got)
		}
		return e
	}
	t.Run("nothing set up", func(t *testing.T) {
		e := populated(t)
		var users, libs, acl int
		count := func() {
			if err := e.db.QueryRow(`SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM libraries), (SELECT count(*) FROM library_acl)`).Scan(&users, &libs, &acl); err != nil {
				t.Fatal(err)
			}
		}
		count()
		u, l, a := users, libs, acl
		run(t, e.db, "0225 up", up)
		run(t, e.db, "0225 up again", up)
		count()
		if got := state(t, e); got != "false" || users != u || libs != l || acl != a {
			t.Fatalf("after up: %s, %d users, %d libraries, %d ACL rows (were %d, %d, %d)", got, users, libs, acl, u, l, a)
		}
		var restricted, defaults int
		if err := e.db.QueryRow(`SELECT (SELECT count(*) FROM parental_policies), (SELECT count(*) FROM libraries WHERE age_tier IS NOT NULL)`).Scan(&restricted, &defaults); err != nil || restricted+defaults != 0 {
			t.Fatalf("the update restricted %d accounts and rated %d libraries (%v)", restricted, defaults, err)
		}
		e.exec(`INSERT INTO app_settings (key, value) VALUES ('parental.notice_dismissed_at', '1'::jsonb)`)
		run(t, e.db, "0225 down", down)
		var left int
		if err := e.db.QueryRow(`SELECT count(*) FROM app_settings WHERE key = 'parental.notice_dismissed_at'`).Scan(&left); err != nil || left != 0 || state(t, e) != "true" {
			t.Fatalf("after down: %s, %d dismissal rows (%v)", state(t, e), left, err)
		}
	})
	t.Run("a restricted account", func(t *testing.T) {
		e := populated(t)
		e.exec(`INSERT INTO parental_policies (user_id, max_tier, version) VALUES ($1, 9, 1)`, uChild)
		run(t, e.db, "0225 up", up)
		if got := state(t, e); got != "true" {
			t.Fatalf("with a restricted account: %s, want true", got)
		}
	})
	t.Run("a library default tier", func(t *testing.T) {
		e := populated(t)
		e.exec(`UPDATE libraries SET age_tier = 0 WHERE id = $1`, libOpen)
		run(t, e.db, "0225 up", up)
		if got := state(t, e); got != "true" {
			t.Fatalf("with a library default tier: %s, want true", got)
		}
	})
}
