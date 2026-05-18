package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrationFiles_Slot0001_HasFoundationalTables is a *static*
// check on the slot-0001 migration: the file exists, both Postgres
// and SQLite siblings ship, and they declare the two foundational
// tables Story 1.5 owns. Real schema-against-Postgres assertions
// live in migrate_integration_test.go (build tag: integration).
func TestMigrationFiles_Slot0001_HasFoundationalTables(t *testing.T) {
	dir := repoMigrationsDir(t)

	for _, name := range []string{
		"0001_init_libraries_and_videos.sql",
		"0001_init_libraries_and_videos.sqlite.sql",
	} {
		path := filepath.Join(dir, name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		s := string(body)
		for _, want := range []string{
			"-- +goose Up",
			"-- +goose Down",
			"CREATE TABLE IF NOT EXISTS libraries",
			"CREATE TABLE IF NOT EXISTS videos",
			"DROP TABLE IF EXISTS videos",
			"DROP TABLE IF EXISTS libraries",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: missing %q", name, want)
			}
		}
	}
}

// TestMigrationFiles_Slot0001_VideoColumns asserts the videos table
// in slot 0001 declares every column architecture.md §8.1 + §8.7
// list as base shape (column extensions from later slots are
// excluded — those land in 0007 and beyond). This catches a class of
// regressions where someone "tidies" the foundational migration and
// drops a column that downstream sqlc queries depend on.
func TestMigrationFiles_Slot0001_VideoColumns(t *testing.T) {
	dir := repoMigrationsDir(t)
	body, err := os.ReadFile(filepath.Join(dir, "0001_init_libraries_and_videos.sql"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(body)
	cols := []string{
		"id",
		"library_id",
		"content_hash",
		"path",
		"filename",
		"size_bytes",
		"mtime",
		"state",
		"detected_language",
		"title",
		"description",
		"poster_path",
		"sprite_path",
		"duration_sec",
		"metadata",
		"created_at",
		"updated_at",
	}
	// Check each column appears in the videos CREATE TABLE block.
	videosBlock := extractCreateBlock(t, s, "videos")
	for _, col := range cols {
		// Look for `<col><whitespace>` to avoid matching `library_id`
		// when checking for `id`. We anchor on word boundaries: a
		// column name followed by space or tab.
		needle := "\n    " + col + " "
		needleAlt := "\n    " + col + "\t"
		if !strings.Contains(videosBlock, needle) && !strings.Contains(videosBlock, needleAlt) {
			t.Errorf("0001 videos table: missing column %q", col)
		}
	}
}

// TestMigrationFiles_Slot0006_HasScanStateAndPurgeLog asserts that
// slot 0006 ships both `library_scan_state` and `purge_log` (per
// architecture.md §8.7 plan-introduced tables: both belong to slot
// 0006, owned by plan-01-05).
func TestMigrationFiles_Slot0006_HasScanStateAndPurgeLog(t *testing.T) {
	dir := repoMigrationsDir(t)
	for _, name := range []string{
		"0006_library_scan_state.sql",
		"0006_library_scan_state.sqlite.sql",
	} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		s := string(body)
		for _, want := range []string{
			"CREATE TABLE IF NOT EXISTS library_scan_state",
			"CREATE TABLE IF NOT EXISTS purge_log",
			"DROP TABLE IF EXISTS purge_log",
			"DROP TABLE IF EXISTS library_scan_state",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: missing %q", name, want)
			}
		}
	}
}

// TestMigrationFiles_Slot0007_AddsLastSeenAndDeletedAt asserts slot
// 0007 adds the soft-delete columns (`last_seen_at`, `deleted_at`)
// and the partial index that backs the straggler-sweep query.
func TestMigrationFiles_Slot0007_AddsLastSeenAndDeletedAt(t *testing.T) {
	dir := repoMigrationsDir(t)
	for _, name := range []string{
		"0007_videos_last_seen_at.sql",
		"0007_videos_last_seen_at.sqlite.sql",
	} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		s := string(body)
		for _, want := range []string{
			"ADD COLUMN IF NOT EXISTS last_seen_at",
			"ADD COLUMN IF NOT EXISTS deleted_at",
			"videos_missing_idx",
			"WHERE state = 'missing'",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: missing %q", name, want)
			}
		}
	}
}

// TestMigrationFiles_PostgresUsesConcurrentlyForCreateIndex enforces
// the migrations/README.md §4 rule: every CREATE INDEX in a
// Postgres-targeted slot 0001+ migration uses CONCURRENTLY (the lint
// also catches this, but we duplicate the check here so the unit
// tier surfaces a regression even when migration-lint is offline,
// e.g. on a developer machine without the tools/migration-lint
// binary on PATH).
func TestMigrationFiles_PostgresUsesConcurrentlyForCreateIndex(t *testing.T) {
	dir := repoMigrationsDir(t)
	files := []string{
		"0001_init_libraries_and_videos.sql",
		"0007_videos_last_seen_at.sql",
	}
	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		s := string(body)
		// Find every CREATE INDEX line. Each one must include
		// CONCURRENTLY before the index name.
		for _, line := range strings.Split(s, "\n") {
			low := strings.ToLower(line)
			if !strings.Contains(low, "create index") &&
				!strings.Contains(low, "create unique index") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "--") {
				continue
			}
			if !strings.Contains(low, "concurrently") {
				t.Errorf("%s: CREATE INDEX without CONCURRENTLY: %q",
					name, strings.TrimSpace(line))
			}
		}
	}
}

// TestMigrationFiles_PostgresAndSqliteSiblingsExist asserts that
// every slot owned by Story 1.5 ships both a Postgres and a SQLite
// file. The migration-lint enforces this globally; we add a focused
// assertion so a regression on Story 1.5's three slots fails the
// fastest test tier.
func TestMigrationFiles_PostgresAndSqliteSiblingsExist(t *testing.T) {
	dir := repoMigrationsDir(t)
	slots := []string{
		"0001_init_libraries_and_videos",
		"0006_library_scan_state",
		"0007_videos_last_seen_at",
	}
	for _, slot := range slots {
		for _, suffix := range []string{".sql", ".sqlite.sql"} {
			path := filepath.Join(dir, slot+suffix)
			if _, err := os.Stat(path); err != nil {
				t.Errorf("missing migration file %s%s: %v", slot, suffix, err)
			}
		}
	}
}

// TestMigrationFiles_Slot0059_AuditLogAppendOnly asserts slot 0059
// ships the append-only guard for `audit_log` on both backends:
//   - the Postgres file declares the guard function + a BEFORE
//     UPDATE OR DELETE trigger (and crucially does NOT guard INSERT,
//     so every existing writer keeps working),
//   - the SQLite parity sibling ships equivalent BEFORE UPDATE /
//     BEFORE DELETE RAISE(ABORT) triggers,
//   - both files carry idempotent down blocks,
//   - partitioning is explicitly deferred (documented in-file) rather
//     than silently dropped.
//
// Static file check, mirroring the rest of this file. Live-DB
// behaviour (UPDATE/DELETE raises, INSERT succeeds) is asserted in
// migrate_integration_test.go.
func TestMigrationFiles_Slot0059_AuditLogAppendOnly(t *testing.T) {
	dir := repoMigrationsDir(t)

	pgPath := filepath.Join(dir, "0059_audit_log_append_only.sql")
	pgBody, err := os.ReadFile(pgPath)
	if err != nil {
		t.Fatalf("read pg slot 0059: %v", err)
	}
	pg := string(pgBody)
	for _, want := range []string{
		"-- +goose Up",
		"-- +goose Down",
		"CREATE OR REPLACE FUNCTION audit_log_no_mutate()",
		"RAISE EXCEPTION",
		"DROP TRIGGER IF EXISTS audit_log_append_only_trg ON audit_log",
		"CREATE OR REPLACE TRIGGER audit_log_append_only_trg",
		"BEFORE UPDATE OR DELETE ON audit_log",
		"DROP FUNCTION IF EXISTS audit_log_no_mutate()",
	} {
		if !strings.Contains(pg, want) {
			t.Errorf("0059 pg: missing %q", want)
		}
	}
	// INSERT must NOT be in the trigger event list — guarding INSERT
	// would break securityaudit.Write / libraries.WriteAudit / the
	// pipeline AuditWriter. The trigger fires on UPDATE OR DELETE only.
	if strings.Contains(pg, "BEFORE INSERT") ||
		strings.Contains(pg, "INSERT OR UPDATE") ||
		strings.Contains(pg, "INSERT OR DELETE") {
		t.Errorf("0059 pg: trigger must not guard INSERT (append path must stay open)")
	}
	// Partitioning is deferred, not silently dropped: the rationale
	// must be in the file.
	if !strings.Contains(strings.ToLower(pg), "partitioning") ||
		!strings.Contains(strings.ToLower(pg), "defer") {
		t.Errorf("0059 pg: partitioning deferral must be documented in-file")
	}

	sqlitePath := filepath.Join(dir, "0059_audit_log_append_only.sqlite.sql")
	sqliteBody, err := os.ReadFile(sqlitePath)
	if err != nil {
		t.Fatalf("read sqlite slot 0059: %v", err)
	}
	sq := string(sqliteBody)
	for _, want := range []string{
		"CREATE TRIGGER IF NOT EXISTS audit_log_no_update_trg",
		"BEFORE UPDATE ON audit_log",
		"CREATE TRIGGER IF NOT EXISTS audit_log_no_delete_trg",
		"BEFORE DELETE ON audit_log",
		"RAISE(ABORT",
		"DROP TRIGGER IF EXISTS audit_log_no_delete_trg",
		"DROP TRIGGER IF EXISTS audit_log_no_update_trg",
	} {
		if !strings.Contains(sq, want) {
			t.Errorf("0059 sqlite: missing %q", want)
		}
	}
}

// TestMigrationFiles_Slot0060_HasIdempotencyKeys asserts slot 0060
// (gap-closure / HLB-315) ships both Postgres and SQLite siblings for
// the durable Idempotency-Key replay store: the table, the
// composite_key primary key that makes concurrent duplicate writes
// race-safe (ON CONFLICT), the reaper index that backs the TTL sweep,
// and a correct down. Real schema-against-Postgres assertions live in
// migrate_integration_test.go (build tag: integration).
func TestMigrationFiles_Slot0060_HasIdempotencyKeys(t *testing.T) {
	dir := repoMigrationsDir(t)
	for _, name := range []string{
		"0060_idempotency_keys.sql",
		"0060_idempotency_keys.sqlite.sql",
	} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		s := string(body)
		for _, want := range []string{
			"-- +goose Up",
			"-- +goose Down",
			"CREATE TABLE IF NOT EXISTS idempotency_keys",
			"composite_key",
			"PRIMARY KEY",
			"request_hash",
			"idempotency_keys_reaper",
			"DROP INDEX IF EXISTS idempotency_keys_reaper",
			"DROP TABLE IF EXISTS idempotency_keys",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: missing %q", name, want)
			}
		}
	}
}

// repoMigrationsDir locates shared/db/migrations relative to the
// test working directory by walking up to the repo root. The Go test
// runner sets cwd to the package directory, so we walk up from
// `api/` to find `shared/db/migrations`.
func repoMigrationsDir(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for cur := cwd; ; {
		candidate := filepath.Join(cur, "shared", "db", "migrations")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			t.Fatalf("could not locate shared/db/migrations from %s", cwd)
		}
		cur = parent
	}
}

// extractCreateBlock returns the substring of `s` from the
// `CREATE TABLE … <name>` clause to the matching `);` so column
// assertions don't false-match on later statements (other tables,
// indexes, etc.).
func extractCreateBlock(t *testing.T, s, table string) string {
	t.Helper()
	needle := "CREATE TABLE IF NOT EXISTS " + table
	i := strings.Index(s, needle)
	if i < 0 {
		t.Fatalf("could not find CREATE TABLE for %s", table)
	}
	// Find the closing `);` after the opening paren.
	open := strings.Index(s[i:], "(")
	if open < 0 {
		t.Fatalf("CREATE TABLE for %s has no opening paren", table)
	}
	open += i
	closeIdx := strings.Index(s[open:], ");")
	if closeIdx < 0 {
		t.Fatalf("CREATE TABLE for %s has no closing paren", table)
	}
	return s[i : open+closeIdx+2]
}
