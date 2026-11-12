package parental_test

// Epic 57 story 57.12: turning parental controls on in a home that already exists,
// against a real migrated Postgres (MAKTABA_TEST_PG_URL).

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
// up starts off; one with a restricted account or a library default tier starts on; a
// value already there is kept; nothing else changes; down removes the key.
func TestSlot0225UpAndDownOnPostgres(t *testing.T) {
	up, down := gooseStatements(t, "0225_parental_enabled.sql")
	// the stored value, "absent" when the key is not there
	state := func(t *testing.T, e *env) string {
		t.Helper()
		var v string
		err := e.db.QueryRow(`SELECT value::text FROM app_settings WHERE key = 'parental.enabled'`).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			return "absent"
		}
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	populated := func(t *testing.T) *env {
		e := setup(t) // users, libraries, an ACL
		run(t, e.db, "0225 down", down)
		if got := state(t, e); got != "absent" {
			t.Fatalf("after down: %s, want the key gone (no slot before 0225 seeds it)", got)
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
		if err := e.db.QueryRow(`SELECT count(*) FROM app_settings WHERE key = 'parental.notice_dismissed_at'`).Scan(&left); err != nil || left != 0 || state(t, e) != "absent" {
			t.Fatalf("after down: %s, %d dismissal rows (%v)", state(t, e), left, err)
		}
	})
	// the slot overwrites nothing: what an admin chose stays, whichever way
	t.Run("a value already there", func(t *testing.T) {
		e := populated(t)
		e.exec(`INSERT INTO app_settings (key, value) VALUES ('parental.enabled', 'true'::jsonb)`)
		run(t, e.db, "0225 up", up)
		if got := state(t, e); got != "true" {
			t.Fatalf("on with nothing set up: %s, want it kept true", got)
		}
		e.exec(`UPDATE app_settings SET value = 'false'::jsonb WHERE key = 'parental.enabled'`)
		e.exec(`UPDATE libraries SET age_tier = 0 WHERE id = $1`, libOpen)
		run(t, e.db, "0225 up again", up)
		if got := state(t, e); got != "false" {
			t.Fatalf("off with a library default tier: %s, want it kept false", got)
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

// Slot 0225's SQLite twin: the same rule, on the desktop build's database.
func TestSlot0225OnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "p.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	for _, dep := range []string{"0029_users.sqlite.sql", "0042_app_settings.sqlite.sql", "0221_parental_policies.sqlite.sql"} {
		depUp, _ := gooseStatements(t, dep)
		run(t, db, dep, depUp)
	}
	// the one column of libraries the slot reads (slot 0220 adds it to the real table)
	run(t, db, "libraries", []string{`CREATE TABLE libraries (id TEXT PRIMARY KEY, age_tier INTEGER)`, `INSERT INTO libraries (id) VALUES ('l1')`})
	up, down := gooseStatements(t, "0225_parental_enabled.sqlite.sql")
	state := func() string {
		t.Helper()
		var v string
		err := db.QueryRow(`SELECT value FROM app_settings WHERE key = 'parental.enabled'`).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			return "absent"
		}
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if state() != "absent" {
		t.Fatalf("before slot 0225: %s, want no key (slot 0221 does not seed it)", state())
	}
	run(t, db, "up", up)
	run(t, db, "up again", up)
	if state() != "false" {
		t.Fatalf("after up with nothing set up: %s, want false", state())
	}
	// a value already there is kept
	run(t, db, "a library default tier", []string{`UPDATE libraries SET age_tier = 0`})
	run(t, db, "up over a value", up)
	if state() != "false" {
		t.Fatalf("up over a stored value: %s, want it kept false", state())
	}
	run(t, db, "down", down)
	if state() != "absent" {
		t.Fatalf("after down: %s, want the key gone", state())
	}
	run(t, db, "up", up)
	if state() != "true" {
		t.Fatalf("with a library default tier: %s, want true", state())
	}
	run(t, db, "a restricted account instead", []string{`DELETE FROM app_settings WHERE key = 'parental.enabled'`,
		`UPDATE libraries SET age_tier = NULL`,
		`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ('c', 's-child', 'x', 0)`,
		`INSERT INTO parental_policies (user_id, max_tier) VALUES ('c', 9)`})
	run(t, db, "up", up)
	if state() != "true" {
		t.Fatalf("with a restricted account: %s, want true", state())
	}
}

// The manifest lists slot 0225 with both of its files.
func TestManifestListsSlot0225(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "MANIFEST.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "| `0225` | `0225_parental_enabled.sql` |") {
		t.Fatal("MANIFEST.md has no row for slot 0225")
	}
	for _, f := range []string{"0225_parental_enabled.sql", "0225_parental_enabled.sqlite.sql"} {
		if _, err := os.Stat(filepath.Join(migrationsDir(t), f)); err != nil {
			t.Fatal(err)
		}
	}
}
