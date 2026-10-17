package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsSQLiteDSN(t *testing.T) {
	for dsn, want := range map[string]bool{
		"sqlite:///tmp/a.db":               true,
		"SQLite3://./a.db":                 true,
		"file:a.db?cache=shared":           true,
		"postgres://u:p@localhost/maktaba": false,
		"":                                 false,
	} {
		if got := isSQLiteDSN(dsn); got != want {
			t.Errorf("isSQLiteDSN(%q) = %v, want %v", dsn, got, want)
		}
	}
}

func TestExecSQLiteStatement_AddColumnIfNotExists(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE t (a TEXT)`); err != nil {
		t.Fatal(err)
	}
	stmt := "-- add b\nALTER TABLE t ADD COLUMN IF NOT EXISTS b TEXT NOT NULL DEFAULT 'x';"
	for i := 0; i < 2; i++ { // the second call must be a no-op, not a "duplicate column" error
		if err := execSQLiteStatement(ctx, db, stmt); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	has, err := sqliteHasColumn(ctx, db, "t", "b")
	if err != nil || !has {
		t.Fatalf("column b missing: has=%v err=%v", has, err)
	}
}

// TestSQLiteMigrate_AppliesEveryTwinToAFreshDatabase runs the real `migrate up`
// path against a new SQLite file: all *.sqlite.sql twins, including the twelve
// `ADD COLUMN IF NOT EXISTS` ones and slot 0004, must apply, and a second run
// must be a no-op.
func TestSQLiteMigrate_AppliesEveryTwinToAFreshDatabase(t *testing.T) {
	if testing.Short() {
		// applies all ~90 twins to a real file twice: seconds, not a unit test.
		// The integration tier (go test without -short) runs it.
		t.Skip("applies every migration to a real SQLite file; integration tier")
	}
	dir := repoMigrationsDir(t)
	path := filepath.Join(t.TempDir(), "maktaba.db")
	dsn := "sqlite://" + filepath.ToSlash(path)
	ctx := context.Background()

	if err := runSQLiteMigrate(ctx, dsn, dir, "up"); err != nil {
		t.Fatalf("first up: %v", err)
	}
	if err := runSQLiteMigrate(ctx, dsn, dir, "up"); err != nil {
		t.Fatalf("second up: %v", err)
	}

	db, err := sql.Open("sqlite", sqliteDriverPath(dsn))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migs, err := listSQLiteMigrations(dir)
	if err != nil || len(migs) < 80 {
		t.Fatalf("expected the full twin set, got %d (err %v)", len(migs), err)
	}
	applied, err := appliedSQLiteVersions(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migs {
		if !applied[m.version] {
			t.Errorf("%s not recorded as applied", m.name)
		}
	}
	for table, col := range map[string]string{
		"videos":          "last_seen_at", // added by 0007 but needed by the 0004 rebuild
		"libraries":       "deleted_at",
		"audit_log":       "occurred_at",
		"users":           "display_name",
		"processing_jobs": "subject_kind", // slot 0088 rebuild, the last twin that used to fail
	} {
		has, err := sqliteHasColumn(ctx, db, table, col)
		if err != nil || !has {
			t.Errorf("%s.%s missing after migrate up (err %v)", table, col, err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + sqliteVersionTable).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(migs) {
		t.Errorf("version rows = %d, want %d (second run must not re-apply)", n, len(migs))
	}
}

func TestSQLiteMigrate_DownIsRefused(t *testing.T) {
	err := runSQLiteMigrate(context.Background(), "sqlite://x.db", t.TempDir(), "down")
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("want a refusal, got %v", err)
	}
}
