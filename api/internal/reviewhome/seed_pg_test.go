package reviewhome_test

// The review home's seeding against a real migrated Postgres (Epic 58 story 58.04). Runs when
// MAKTABA_TEST_PG_URL names a Postgres the user may create databases on (CI's integration tier).

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/reviewhome"
)

var accounts = reviewhome.Accounts{
	Viewer: "reviewer", ViewerPassword: "viewer-test-pass-0001",
	Child: "reviewer-child", ChildPassword: "child-test-pass-00002",
}

func manifest() *reviewhome.Manifest {
	return &reviewhome.Manifest{
		Schema: 1, MediaRoot: "/srv/review-media",
		Libraries: []reviewhome.ManifestLibrary{
			{ID: "films", Dir: "films", NameEN: "Films", NameAR: "أفلام", AgeTier: 0},
			{ID: "films-older", Dir: "films-older", NameEN: "Films for older viewers", NameAR: "أفلام للكبار", AgeTier: 13},
		},
		Channel: reviewhome.ManifestChannel{NameEN: "Review TV", NameAR: "قناة المراجعة", Library: "films", Number: 1},
		Items: []reviewhome.ManifestItem{
			{ID: "spring", Library: "films", File: "spring.mp4", TitleEN: "Spring", TitleAR: "الربيع",
				Licence: "CC-BY-4.0", LicenceURL: "https://creativecommons.org/licenses/by/4.0/", Attribution: "Spring, Blender Animation Studio (2019)"},
			{ID: "sintel", Library: "films-older", File: "sintel.mp4", TitleEN: "Sintel", TitleAR: "سينتل",
				Licence: "CC-BY-3.0", LicenceURL: "https://creativecommons.org/licenses/by/3.0/", Attribution: "Sintel, Blender Foundation (2010)"},
		},
	}
}

func one[T any](t *testing.T, db *sql.DB, q string, args ...any) T {
	t.Helper()
	var v T
	if err := db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

func TestSeedMakesTheReviewHome(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := clustertest.ScratchDB(t)
	m := manifest()
	rep, err := reviewhome.Seed(ctx, db, m, accounts)
	if err != nil {
		t.Fatal(err)
	}

	// the libraries, at their folders and tiers, each with a recompute job for its videos
	if got := one[int](t, db, `SELECT count(*) FROM libraries WHERE roots = ARRAY['/srv/review-media/films'] AND age_tier = 0 AND name = 'Films · أفلام'`); got != 1 {
		t.Errorf("the films library: %d rows", got)
	}
	if got := one[int](t, db, `SELECT age_tier FROM libraries WHERE id = $1`, rep.Libraries["films-older"]); got != 13 {
		t.Errorf("the older films' tier = %d, want 13", got)
	}
	if got := one[int](t, db, `SELECT count(*) FROM parental_recompute_jobs WHERE kind = 'library'`); got < 2 {
		t.Errorf("recompute jobs = %d, want one per library", got)
	}

	// the viewer: not an admin, every library, no limit; the password works
	us := users.New(db)
	v, err := us.GetByUsername(ctx, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if v.IsAdmin {
		t.Fatal("the reviewer is an admin")
	}
	if err := v.VerifyPassword(accounts.ViewerPassword); err != nil {
		t.Errorf("the viewer's password: %v", err)
	}
	if got := one[int](t, db, `SELECT count(*) FROM library_acl WHERE user_id = $1`, v.ID); got != 2 {
		t.Errorf("the viewer's libraries = %d, want 2", got)
	}
	ps := parental.NewStore(db)
	if r, err := ps.IsRestricted(ctx, v.ID); err != nil || r {
		t.Errorf("the viewer is restricted (%v, %v): it must show everything", r, err)
	}

	// the child: 9+, unrated hidden, every library, the viewer as its guardian
	c, err := us.GetByUsername(ctx, "reviewer-child")
	if err != nil {
		t.Fatal(err)
	}
	p, err := ps.Policy(ctx, c.ID)
	if err != nil || p.MaxTier != parental.Tier9 || p.Unrated != parental.UnratedHide || p.AllowOnly {
		t.Fatalf("the child's policy = %+v (%v), want 9+ with unrated hidden", p, err)
	}
	if g, err := ps.IsGuardian(ctx, v.ID, c.ID); err != nil || !g {
		t.Errorf("the reviewer looks after the child: %v, %v", g, err)
	}
	if got := one[int](t, db, `SELECT count(*) FROM library_acl WHERE user_id = $1`, c.ID); got != 2 {
		t.Errorf("the child's libraries = %d, want 2", got)
	}

	// the live channel: a shuffle of the films, stale for the scheduler
	if got := one[string](t, db, `SELECT mode FROM channels WHERE id = $1 AND library_id = $2`, rep.ChannelID, rep.Libraries["films"]); got != "shuffle" {
		t.Errorf("the channel's mode = %q", got)
	}
	if got := one[bool](t, db, `SELECT stale FROM channel_schedule_state WHERE channel_id = $1`, rep.ChannelID); !got {
		t.Error("the channel is not marked for scheduling")
	}

	// nothing scanned yet: both items are missing, nothing titled
	if len(rep.Missing) != 2 || rep.Titled != 0 {
		t.Errorf("before the scan: missing %v, titled %d", rep.Missing, rep.Titled)
	}

	// the scan finds the files; seeding again titles them, and changes nothing else
	for lib, file := range map[string]string{"films": "spring.mp4", "films-older": "sintel.mp4"} {
		if _, err := db.Exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state)
			VALUES (gen_random_uuid(), $1, '/srv/review-media/' || $2, $2, encode(sha256($2::bytea), 'hex'), 1, now(), 'ready')`, rep.Libraries[lib], file); err != nil {
			t.Fatal(err)
		}
	}
	again, err := reviewhome.Seed(ctx, db, m, accounts)
	if err != nil {
		t.Fatal(err)
	}
	if again.ViewerID != rep.ViewerID || again.ChildID != rep.ChildID || again.ChannelID != rep.ChannelID {
		t.Errorf("a second seed made new rows: %+v then %+v", rep, again)
	}
	if len(again.Missing) != 0 || again.Titled != 2 {
		t.Errorf("after the scan: missing %v, titled %d", again.Missing, again.Titled)
	}
	title := one[string](t, db, `SELECT title FROM videos WHERE filename = 'spring.mp4'`)
	desc := one[string](t, db, `SELECT description FROM videos WHERE filename = 'spring.mp4'`)
	if title != "Spring · الربيع" || desc != m.Items[0].LicenceNote() {
		t.Errorf("spring: %q / %q", title, desc)
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM users WHERE username IN ('reviewer', 'reviewer-child')`: 2,
		`SELECT count(*) FROM libraries`:                                              2,
		`SELECT count(*) FROM channels`:                                               1,
		`SELECT count(*) FROM parental_guardians`:                                     1,
	} {
		if got := one[int](t, db, q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}
}

func TestSeedNeverUsesAnAdmin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := clustertest.ScratchDB(t)
	if _, err := users.New(db).Create(ctx, users.CreateInput{Username: "reviewer", Password: "someone-elses-pass-1", IsAdmin: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewhome.Seed(ctx, db, manifest(), accounts); !errors.Is(err, reviewhome.ErrAdmin) {
		t.Fatalf("seed over an admin named reviewer = %v, want ErrAdmin", err)
	}
	if err := reviewhome.Rotate(ctx, db, accounts); !errors.Is(err, reviewhome.ErrAdmin) {
		t.Fatalf("rotate over an admin = %v, want ErrAdmin", err)
	}
	u, _ := users.New(db).GetByUsername(ctx, "reviewer")
	if err := u.VerifyPassword("someone-elses-pass-1"); err != nil {
		t.Error("the admin's password was changed")
	}
}

func TestSeedPutsTheChildsLimitBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := clustertest.ScratchDB(t)
	rep, err := reviewhome.Seed(ctx, db, manifest(), accounts)
	if err != nil {
		t.Fatal(err)
	}
	// someone loosened the child's limit and made the viewer restricted
	if _, err := db.Exec(`UPDATE parental_policies SET max_tier = 18 WHERE user_id = $1`, rep.ChildID); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewhome.Seed(ctx, db, manifest(), accounts); err != nil {
		t.Fatal(err)
	}
	p, err := parental.NewStore(db).Policy(ctx, rep.ChildID)
	if err != nil || p.MaxTier != parental.Tier9 {
		t.Errorf("the child's limit after a second seed = %+v (%v), want 9+", p, err)
	}
}

func TestRotateSignsEveryDeviceOut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := clustertest.ScratchDB(t)
	rep, err := reviewhome.Seed(ctx, db, manifest(), accounts)
	if err != nil {
		t.Fatal(err)
	}
	// a TV a store's reviewer paired, and a browser
	for i, u := range []string{rep.ViewerID, rep.ChildID} {
		if _, err := db.Exec(`INSERT INTO refresh_tokens (id, user_id, hash, family_id, expires_at)
			VALUES (gen_random_uuid(), $1, 'h-' || $2::text, gen_random_uuid(), now() + interval '30 days')`, u, i); err != nil {
			t.Fatal(err)
		}
	}
	next := reviewhome.Accounts{Viewer: "reviewer", ViewerPassword: "the-next-round-pass-1", Child: "reviewer-child", ChildPassword: "the-next-round-pass-22"}
	if err := reviewhome.Rotate(ctx, db, next); err != nil {
		t.Fatal(err)
	}
	us := users.New(db)
	for name, pw := range map[string][2]string{"reviewer": {next.ViewerPassword, accounts.ViewerPassword}, "reviewer-child": {next.ChildPassword, accounts.ChildPassword}} {
		u, err := us.GetByUsername(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := u.VerifyPassword(pw[0]); err != nil {
			t.Errorf("%s: the new password: %v", name, err)
		}
		if err := u.VerifyPassword(pw[1]); err == nil {
			t.Errorf("%s: the old password still works", name)
		}
	}
	if got := one[int](t, db, `SELECT count(*) FROM refresh_tokens WHERE revoked_at IS NULL`); got != 0 {
		t.Errorf("%d devices still signed in after the rotation", got)
	}
	// a rotation with an account missing changes nothing it cannot finish
	if err := reviewhome.Rotate(ctx, db, reviewhome.Accounts{Viewer: "nobody-here", ViewerPassword: "a-long-pass-for-nobody", Child: "reviewer-child", ChildPassword: "x-long-pass-for-the-kid"}); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("rotate with no such account = %v, want ErrNotFound", err)
	}
	// nor with the child missing: the viewer keeps the password it had
	if err := reviewhome.Rotate(ctx, db, reviewhome.Accounts{Viewer: "reviewer", ViewerPassword: "a-third-round-pass-01", Child: "nobody-here", ChildPassword: "x-long-pass-for-the-kid"}); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("rotate with no such child = %v, want ErrNotFound", err)
	}
	if u, err := us.GetByUsername(ctx, "reviewer"); err != nil || u.VerifyPassword(next.ViewerPassword) != nil {
		t.Errorf("a rotation that could not finish changed the viewer's password (%v)", err)
	}
}
