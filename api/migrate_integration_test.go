//go:build integration

package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

// TestMigrate_Slot0001Through0007_AppliesAndRollsBack is the
// integration-tier sanity check for Story 1.5 migrations. It runs
// goose up to apply 0001/0006/0007, asserts the resulting schema
// matches architecture.md §8.1 + §8.7, then runs goose down to
// confirm rollback is clean.
//
// Tier: integration (build tag). The CI _integration.yml workflow
// provisions postgres:16.4 and exports DATABASE_URL.
//
// Locally: `DATABASE_URL=postgres://maktaba:maktaba@localhost:5432/maktaba?sslmode=disable
//
//	go test -tags=integration ./api -run TestMigrate`
func TestMigrate_Slot0001Through0007_AppliesAndRollsBack(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset: skipping migration integration test")
	}
	db := openTestDB(t, dsn)
	t.Cleanup(func() { db.Close() })
	resetSchema(t, db)

	dir := repoMigrationsDir(t)
	stage, err := stagePostgresMigrations(dir)
	if err != nil {
		t.Fatalf("stage migrations: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(stage) })

	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// --- Up to slot 0007 (the last Story 1.5 slot) ---
	if err := goose.UpToContext(ctx, db, stage, 7); err != nil {
		t.Fatalf("goose up to 7: %v", err)
	}
	v, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if v != 7 {
		t.Fatalf("after up: schema version = %d, want 7", v)
	}

	// --- Schema assertions ---
	assertTableExists(t, db, "libraries")
	assertTableExists(t, db, "videos")
	assertTableExists(t, db, "library_scan_state")
	assertTableExists(t, db, "purge_log")

	// videos columns from slot 0001 + slot 0007.
	for _, col := range []string{
		"id", "library_id", "content_hash", "path", "filename",
		"size_bytes", "mtime", "state", "detected_language", "title",
		"description", "poster_path", "sprite_path", "duration_sec",
		"metadata", "created_at", "updated_at",
		"last_seen_at", "deleted_at",
	} {
		assertColumnExists(t, db, "videos", col)
	}

	// libraries columns from slot 0001.
	for _, col := range []string{
		"id", "name", "roots", "settings", "created_at", "updated_at",
	} {
		assertColumnExists(t, db, "libraries", col)
	}

	// Indexes from slot 0001 + slot 0007.
	for _, idx := range []string{
		"videos_library_state_idx",
		"videos_library_path_idx",
		"videos_detected_language_idx",
		"videos_missing_idx",
	} {
		assertIndexExists(t, db, idx)
	}

	// Slot 0001 ships content_hash UNIQUE (global); slot 0003 (owned
	// by plan-01-02) drops that and adds UNIQUE (library_id,
	// content_hash) as the `videos_library_content_hash_key` index
	// (kept as an index rather than a table-constraint so the CREATE
	// can use CONCURRENTLY — see migration's inline comment). After
	// running through slot 7 we should see the new composite index and
	// not the original global table-constraint.
	assertConstraintAbsent(t, db, "videos", "videos_content_hash_key")
	assertIndexExists(t, db, "videos_library_content_hash_key")

	// --- Idempotency: re-run up. Goose should treat already-applied
	// versions as no-ops; UpToContext returns nil with no DDL emitted.
	if err := goose.UpToContext(ctx, db, stage, 7); err != nil {
		t.Fatalf("goose up (idempotency): %v", err)
	}
	v2, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		t.Fatalf("get version after idempotency: %v", err)
	}
	if v2 != 7 {
		t.Fatalf("after idempotent up: version = %d, want 7", v2)
	}

	// --- Down to 0 (drop everything Story 1.5 added). ---
	if err := goose.DownToContext(ctx, db, stage, 0); err != nil {
		t.Fatalf("goose down to 0: %v", err)
	}
	v3, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		t.Fatalf("get version after down: %v", err)
	}
	if v3 != 0 {
		t.Fatalf("after down: version = %d, want 0", v3)
	}
	assertTableAbsent(t, db, "libraries")
	assertTableAbsent(t, db, "videos")
	assertTableAbsent(t, db, "library_scan_state")
	assertTableAbsent(t, db, "purge_log")
}

// TestMigrate_Slot0001_VideosCascadeDeleteFromLibrary verifies the
// ON DELETE CASCADE on videos.library_id fires correctly. This is a
// deliberate end-to-end check — the FK declaration in the SQL is
// useless if the deploy DB doesn't actually enforce it.
func TestMigrate_Slot0001_VideosCascadeDeleteFromLibrary(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset")
	}
	db := openTestDB(t, dsn)
	t.Cleanup(func() { db.Close() })
	resetSchema(t, db)

	dir := repoMigrationsDir(t)
	stage, err := stagePostgresMigrations(dir)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(stage) })

	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := goose.UpToContext(ctx, db, stage, 7); err != nil {
		t.Fatalf("goose up: %v", err)
	}

	// Insert one library + one video pointing at it.
	var libID string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO libraries (name, roots) VALUES ('test', ARRAY['/tmp/test']) RETURNING id`,
	).Scan(&libID); err != nil {
		t.Fatalf("insert library: %v", err)
	}
	// Slot 0003's `videos_content_hash_format_chk` CHECK constrains the
	// hash to 64 lower-hex chars (sha256 shape); use a real sha256-
	// shaped string rather than a short sentinel.
	_, err = db.ExecContext(ctx, `
		INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime)
		VALUES ($1, 'deadbeef00000000000000000000000000000000000000000000000000000000', '/tmp/test/x.mkv', 'x.mkv', 12345, now())
	`, libID)
	if err != nil {
		t.Fatalf("insert video: %v", err)
	}

	// Delete the library: videos row must cascade.
	if _, err := db.ExecContext(ctx, `DELETE FROM libraries WHERE id = $1`, libID); err != nil {
		t.Fatalf("delete library: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM videos`).Scan(&n); err != nil {
		t.Fatalf("count videos: %v", err)
	}
	if n != 0 {
		t.Fatalf("after library delete: %d videos remain, want 0 (CASCADE failed)", n)
	}
}

// --- helpers ---

// openTestDB opens dsn and pings to make sure the connection is
// usable before we start handing it to goose.
func openTestDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return db
}

// resetSchema drops every Story 1.5–era table and the goose
// bookkeeping table so each test starts from a clean slate.
// Order matters: child tables before parents (for the FK to
// libraries).
func resetSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stmts := []string{
		`DROP TABLE IF EXISTS purge_log CASCADE`,
		`DROP TABLE IF EXISTS library_scan_state CASCADE`,
		`DROP TABLE IF EXISTS videos CASCADE`,
		`DROP TABLE IF EXISTS libraries CASCADE`,
		`DROP TABLE IF EXISTS maktaba_schema_version CASCADE`,
		`DROP TABLE IF EXISTS goose_db_version CASCADE`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("reset %q: %v", s, err)
		}
	}
}

func assertTableExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	if !tableExists(t, db, name) {
		t.Errorf("table %q missing", name)
	}
}

func assertTableAbsent(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	if tableExists(t, db, name) {
		t.Errorf("table %q present, want absent", name)
	}
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = $1
	`, name).Scan(&n)
	if err != nil {
		t.Fatalf("query tables for %s: %v", name, err)
	}
	return n > 0
}

func assertColumnExists(t *testing.T, db *sql.DB, table, column string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = $1 AND column_name = $2
	`, table, column).Scan(&n)
	if err != nil {
		t.Fatalf("query columns for %s.%s: %v", table, column, err)
	}
	if n == 0 {
		t.Errorf("column %s.%s missing", table, column)
	}
}

func assertIndexExists(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = current_schema() AND indexname = $1
	`, name).Scan(&n)
	if err != nil {
		t.Fatalf("query indexes for %s: %v", name, err)
	}
	if n == 0 {
		t.Errorf("index %s missing", name)
	}
}

func assertConstraintExists(t *testing.T, db *sql.DB, table, constraint string) {
	t.Helper()
	if !constraintExists(t, db, table, constraint) {
		t.Errorf("constraint %s on %s missing", constraint, table)
	}
}

func assertConstraintAbsent(t *testing.T, db *sql.DB, table, constraint string) {
	t.Helper()
	if constraintExists(t, db, table, constraint) {
		t.Errorf("constraint %s on %s present, want absent", constraint, table)
	}
}

func constraintExists(t *testing.T, db *sql.DB, table, constraint string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM information_schema.table_constraints
		WHERE table_schema = current_schema()
		  AND table_name = $1 AND constraint_name = $2
	`, table, constraint).Scan(&n)
	if err != nil {
		t.Fatalf("query constraints for %s.%s: %v", table, constraint, err)
	}
	return n > 0
}

// dsnRedactSanity is a guard that runs at package load to surface
// configuration mistakes early — if DATABASE_URL points at a
// production host (heuristic: contains "prod" in the host) we abort
// every test in this file rather than running migrations against it.
func init() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return
	}
	low := strings.ToLower(dsn)
	if strings.Contains(low, "@prod") || strings.Contains(low, ".prod.") ||
		strings.Contains(low, "production") {
		panic("DATABASE_URL appears to point at production; refusing to run migration integration tests")
	}
}
