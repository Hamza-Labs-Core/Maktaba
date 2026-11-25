//go:build integration

package db_test

import (
	"context"
	"io/fs"
	"os"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// TestUpAppliesALowerSlotLate: an epic's migration numbered below the database's version
// (Epic 50's 0050 landing after Epic 51's 0051 was applied) is still applied by Up.
func TestUpAppliesALowerSlotLate(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	// the database as it was before 0050 shipped: 0051 applied, 0050 not
	if _, err := pool.DB.ExecContext(ctx, `DELETE FROM reserved_slugs WHERE reason = 'infrastructure'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.DB.ExecContext(ctx, `DELETE FROM goose_db_version WHERE version_id = 500001`); err != nil {
		t.Fatal(err)
	}
	if err := db.NewMigrator(pool, migrations.FS, ".").Up(ctx); err != nil {
		t.Fatalf("Up with a lower slot missing: %v", err)
	}
	var n int
	if err := pool.DB.QueryRowContext(ctx, `SELECT count(*) FROM reserved_slugs WHERE slug IN ('canary','r42')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("reserved infra slugs after a late Up = %d, want 2", n)
	}
}

// A database that had a later slot when a lower one landed is at head once Up applied it (v0.3.163 was
// never ready on such a database: goose's current version is the one applied last), and only then.
func TestAtHeadWithALowerSlotAppliedLate(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	m := db.NewMigrator(pool, migrations.FS, ".")
	at := func(want bool, when string) {
		t.Helper()
		if got, err := m.AtHead(ctx); err != nil || got != want {
			t.Fatalf("%s: at head %v %v, want %v", when, got, err, want)
		}
	}
	at(true, "migrated in order")
	// as the release before the late one left it
	if _, err := pool.DB.ExecContext(ctx, `ALTER TABLE server_claims DROP COLUMN retry_key_hash`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.DB.ExecContext(ctx, `DELETE FROM goose_db_version WHERE version_id = 40002`); err != nil {
		t.Fatal(err)
	}
	at(false, "a lower slot missing")
	if err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	at(true, "the lower slot applied last")
	// a schema ahead of the binary (a release that went back and kept it) is at head for it
	if _, err := pool.DB.ExecContext(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (99999999, true)`); err != nil {
		t.Fatal(err)
	}
	at(true, "a later release's migration beside its own")
	if _, err := pool.DB.ExecContext(ctx, `DROP TABLE goose_db_version CASCADE`); err != nil {
		t.Fatal(err)
	}
	at(false, "a database with no migrations")
}

// recordedLast is what a release up to v0.3.163 takes for the schema's version, and what the newest
// applied migration is.
func recordedLast(t *testing.T, pool *db.Pool) (last, newest int64) {
	t.Helper()
	if err := pool.DB.QueryRow(`SELECT (SELECT version_id FROM goose_db_version ORDER BY id DESC LIMIT 1), max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&last, &newest); err != nil {
		t.Fatal(err)
	}
	return last, newest
}

// Up records the newest migration last, whatever it applied, and so does Undo; releases up to v0.3.163 are
// ready only on such a database (one that went back on a kept schema was held: never ready).
func TestUpRecordsTheNewestLast(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	if _, err := pool.DB.ExecContext(ctx, `DELETE FROM goose_db_version WHERE version_id > 0`); err != nil {
		t.Fatal(err)
	}
	mig := func(name string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE " + name + " (id INT);\n-- +goose Down\nDROP TABLE " + name + ";\n")}
	}
	set := fstest.MapFS{"00400001_a.sql": mig("na"), "00580001_b.sql": mig("nb")}
	m := db.NewMigrator(pool, set, ".")
	up := func(when string) {
		t.Helper()
		if err := m.Up(ctx); err != nil {
			t.Fatalf("%s: %v", when, err)
		}
		last, newest := recordedLast(t, pool)
		if at, err := m.AtHead(ctx); err != nil || !at || last != newest {
			t.Fatalf("%s: recorded last %d, the newest is %d, at head %v %v", when, last, newest, at, err)
		}
	}
	applied := func(want ...int64) {
		t.Helper()
		got, err := m.Applied(ctx)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("applied %v %v, want %v", got, err, want)
		}
	}
	up("in order")
	applied(400001, 580001)
	// a lower slot late
	set["00400002_late.sql"] = mig("nlate")
	up("a lower slot late")
	applied(400001, 400002, 580001)
	// and again: nothing moves
	var before, after int64
	rows := func(v *int64) {
		if err := pool.DB.QueryRow(`SELECT max(id) FROM goose_db_version`).Scan(v); err != nil {
			t.Fatal(err)
		}
	}
	rows(&before)
	up("nothing to do")
	if rows(&after); after != before {
		t.Fatalf("a row was moved for nothing: %d, then %d", before, after)
	}
	// a late one and a newer one together, then both undone (the release before goes on)
	set["00400003_late.sql"], set["00580002_new.sql"] = mig("nlate3"), mig("nnew")
	up("a lower slot and a newer one")
	applied(400001, 400002, 580001, 400003, 580002)
	if err := m.Undo(ctx, []int64{400003, 580002}); err != nil {
		t.Fatal(err)
	}
	applied(400001, 400002, 580001)
	// the newer one undone alone, beside a late one that stays
	up("both again")
	if err := m.Undo(ctx, []int64{580002}); err != nil {
		t.Fatal(err)
	}
	if last, newest := recordedLast(t, pool); last != 580001 || newest != 580001 {
		t.Fatalf("after undo: recorded last %d, the newest is %d", last, newest)
	}
	// the late ones undone alone, beside a newer one that stays
	up("once more")
	if err := m.Undo(ctx, []int64{400002, 400003}); err != nil {
		t.Fatal(err)
	}
	applied(400001, 580001, 580002)
	// as v0.3.163 left it: the late one recorded last, and nothing to apply
	delete(set, "00400002_late.sql")
	delete(set, "00400003_late.sql")
	if _, err := pool.DB.ExecContext(ctx, `DELETE FROM goose_db_version WHERE version_id = 400001`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.DB.ExecContext(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (400001, true)`); err != nil {
		t.Fatal(err)
	}
	up("what v0.3.163 recorded")
	applied(580001, 400001, 580002)
}

// The database of the last release (its cloud/migrations, tools/ci/last_release_migrations.sh), migrated
// by this commit: ready, and the last release is too should it go back on what was kept.
func TestUpFromTheLastRelease(t *testing.T) {
	dir := os.Getenv("MAKTABA_LAST_RELEASE_MIGRATIONS")
	if dir == "" {
		t.Skip("MAKTABA_LAST_RELEASE_MIGRATIONS is not set (CI sets it)")
	}
	ctx := context.Background()
	before := os.DirFS(dir)
	files, err := fs.Glob(before, "*.sql")
	if err != nil || len(files) < 10 {
		t.Fatalf("%s: %d migrations, %v", dir, len(files), err)
	}
	pool := dbtest.Empty(t)
	was := db.NewMigrator(pool, before, ".")
	if err := was.Up(ctx); err != nil {
		t.Fatalf("the last release's: %v", err)
	}
	now := db.NewMigrator(pool, migrations.FS, ".")
	if err := now.Up(ctx); err != nil {
		t.Fatalf("this commit's, over the last release's: %v", err)
	}
	for who, m := range map[string]*db.Migrator{"this commit": now, "the last release": was} {
		if at, err := m.AtHead(ctx); err != nil || !at {
			t.Fatalf("%s is not at head: %v %v", who, at, err)
		}
	}
	if last, newest := recordedLast(t, pool); last != newest {
		t.Fatalf("recorded last %d, the newest is %d: a release up to v0.3.163 would not be ready", last, newest)
	}
}
