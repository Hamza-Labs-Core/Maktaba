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

// TestMigrationFiles_Slot0056_LicensesShape asserts slot 0056 ships
// the `licenses` table with the columns the persistent subscriptions
// store (HLB-287) reads/writes, plus the one-active-license invariant
// (the partial unique index on the Postgres sibling). This guards the
// schema the entitlement-persistence code depends on against a
// "tidy-up" regression.
func TestMigrationFiles_Slot0056_LicensesShape(t *testing.T) {
	dir := repoMigrationsDir(t)

	pg, err := os.ReadFile(filepath.Join(dir, "0056_licenses.sql"))
	if err != nil {
		t.Fatalf("read 0056_licenses.sql: %v", err)
	}
	pgs := string(pg)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS licenses",
		"license_id",
		"tier",
		"seats",
		"issued_at",
		"expires_at",
		"revoked_at",
		"raw_jwt",
		"features",
		"licenses_only_one_active",
		"WHERE revoked_at IS NULL",
		"DROP TABLE IF EXISTS licenses",
	} {
		if !strings.Contains(pgs, want) {
			t.Errorf("0056_licenses.sql: missing %q", want)
		}
	}

	lite, err := os.ReadFile(filepath.Join(dir, "0056_licenses.sqlite.sql"))
	if err != nil {
		t.Fatalf("read 0056_licenses.sqlite.sql: %v", err)
	}
	lites := string(lite)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS licenses",
		"license_id",
		"raw_jwt",
		"revoked_at",
		"DROP TABLE IF EXISTS licenses",
	} {
		if !strings.Contains(lites, want) {
			t.Errorf("0056_licenses.sqlite.sql: missing %q", want)
		}
	}
}

// TestMigrationFiles_Slot0063_TierDomainFreeHomePro asserts slot 0063
// widens licenses.tier to the spec's free/home/pro model (Epic 16
// Story 16.2). Guards against a regression that would re-narrow the
// CHECK and reject legitimate home/pro license rows.
func TestMigrationFiles_Slot0063_TierDomainFreeHomePro(t *testing.T) {
	dir := repoMigrationsDir(t)

	pg, err := os.ReadFile(filepath.Join(dir, "0063_licenses_tier_free_home_pro.sql"))
	if err != nil {
		t.Fatalf("read 0063 pg: %v", err)
	}
	pgs := string(pg)
	for _, want := range []string{
		"DROP CONSTRAINT IF EXISTS licenses_tier_check",
		"CHECK (tier IN ('free', 'home', 'pro'))",
		"pg_constraint",
		"UPDATE licenses SET tier = 'pro' WHERE tier = 'premium'",
		"-- +goose Down",
		"CHECK (tier IN ('free', 'premium'))", // down restores 0056 domain
	} {
		if !strings.Contains(pgs, want) {
			t.Errorf("0063 pg: missing %q", want)
		}
	}

	lite, err := os.ReadFile(filepath.Join(dir, "0063_licenses_tier_free_home_pro.sqlite.sql"))
	if err != nil {
		t.Fatalf("read 0063 sqlite: %v", err)
	}
	lites := string(lite)
	for _, want := range []string{
		"CHECK (tier IN ('free', 'home', 'pro'))",
		"DROP TABLE IF EXISTS licenses",
		"RENAME TO licenses",
		"-- +goose Down",
	} {
		if !strings.Contains(lites, want) {
			t.Errorf("0063 sqlite: missing %q", want)
		}
	}
}

// TestMigrationFiles_Slot0055_PairingTicketsStoreColumns binds the
// column list SQLPairingStore reads/writes to the slot-0055 migration
// text. This is the cheap schema-binding guard the discovery store unit
// tests reference (no DB needed): if someone renames or drops a column
// in the migration without updating internal/discovery/sqlstore.go (or
// vice versa) this fails, instead of the drift only surfacing in an
// integration run. It also pins the partial index Sweep's hot path
// relies on, so removing pairing_tickets_reaper resurrects the every-30s
// whole-table sequential scan loudly.
func TestMigrationFiles_Slot0055_PairingTicketsStoreColumns(t *testing.T) {
	dir := repoMigrationsDir(t)

	pg, err := os.ReadFile(filepath.Join(dir, "0055_pairing_tickets.sql"))
	if err != nil {
		t.Fatalf("read 0055_pairing_tickets.sql: %v", err)
	}
	block := extractCreateBlock(t, string(pg), "pairing_tickets")
	// Every column SQLPairingStore.{Put,Get,Consume,Sweep} names must
	// exist in the table definition.
	for _, col := range []string{
		"code",
		"user_id",
		"issued_at",
		"expires_at",
		"consumed_at",
	} {
		if !strings.Contains(block, col) {
			t.Errorf("0055_pairing_tickets.sql pairing_tickets table: missing column %q "+
				"(SQLPairingStore reads/writes it)", col)
		}
	}
	// Sweep's index-aligned hot path requires this exact partial index;
	// dropping the predicate would silently reintroduce the seq-scan.
	for _, want := range []string{
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS pairing_tickets_reaper",
		"ON pairing_tickets (expires_at) WHERE consumed_at IS NULL",
	} {
		if !strings.Contains(string(pg), want) {
			t.Errorf("0055_pairing_tickets.sql: missing %q (Sweep's partial-index hot path)", want)
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
// the durable Idempotency-Key replay store: the table, the TWO-COLUMN
// (user_id, idem_key) composite primary key that makes concurrent
// duplicate writes race-safe (ON CONFLICT) WITHOUT a NUL-joined
// string (Postgres TEXT rejects 0x00 — W1-C3 hotfix), the reaper
// index that backs the TTL sweep, and a correct down. Real
// schema-against-Postgres assertions live in
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
			"PRIMARY KEY (user_id, idem_key)",
			"request_hash",
			"idempotency_keys_reaper",
			"DROP INDEX IF EXISTS idempotency_keys_reaper",
			"DROP TABLE IF EXISTS idempotency_keys",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: missing %q", name, want)
			}
		}
		// The NUL-joined composite_key column must be gone: it was
		// unstorable on Postgres TEXT (the W1-C3 prod/CI bug).
		if strings.Contains(s, "composite_key") {
			t.Errorf("%s: composite_key column must be removed "+
				"(Postgres TEXT rejects the 0x00 separator)", name)
		}
	}
}

// TestMigrationFiles_Slot0061_HasEventsBus asserts slot 0061
// (gap-closure Wave 2 / Epic 19 Story 19.2 / HLB-353) ships both
// Postgres and SQLite siblings for the cross-replica WS event bus:
// the durable append-only `events` table with the monotonic id +
// channel/type/payload, the replay and pruner indexes, and — on
// Postgres only — the bounded pg_notify('ws.events',…) trigger that
// keeps the NOTIFY frame under the 8 KiB limit while the full payload
// stays in the table. Real schema-against-Postgres + the live
// cross-replica/replay flow are exercised in eventbus_integration_test.go
// (build tag: integration).
func TestMigrationFiles_Slot0061_HasEventsBus(t *testing.T) {
	dir := repoMigrationsDir(t)

	pg, err := os.ReadFile(filepath.Join(dir, "0061_events.sql"))
	if err != nil {
		t.Fatalf("read 0061_events.sql: %v", err)
	}
	pgs := string(pg)
	for _, want := range []string{
		"-- +goose NO TRANSACTION",
		"-- +goose Up",
		"-- +goose Down",
		"CREATE TABLE IF NOT EXISTS events",
		"id          BIGSERIAL    PRIMARY KEY",
		"channel     TEXT         NOT NULL",
		"payload     JSONB",
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS events_channel_id",
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS events_created_at",
		"CREATE OR REPLACE FUNCTION events_notify()",
		"pg_notify(",
		"'ws.events'",
		"CREATE OR REPLACE TRIGGER events_notify_trg",
		"DROP TRIGGER IF EXISTS events_notify_trg ON events",
		"DROP FUNCTION IF EXISTS events_notify()",
		"DROP TABLE IF EXISTS events",
	} {
		if !strings.Contains(pgs, want) {
			t.Errorf("0061_events.sql: missing %q", want)
		}
	}
	// The bounded-NOTIFY contract (Story 19.2 AC3): the trigger must
	// notify with id + channel only — never the payload — so the
	// frame can't exceed Postgres' 8 KiB NOTIFY bound.
	if strings.Contains(pgs, "NEW.payload") {
		t.Errorf("0061_events.sql: NOTIFY must not carry the payload (8 KiB bound) — found NEW.payload near pg_notify")
	}

	sl, err := os.ReadFile(filepath.Join(dir, "0061_events.sqlite.sql"))
	if err != nil {
		t.Fatalf("read 0061_events.sqlite.sql: %v", err)
	}
	sls := string(sl)
	for _, want := range []string{
		"-- +goose Up",
		"-- +goose Down",
		"CREATE TABLE IF NOT EXISTS events",
		"INTEGER     PRIMARY KEY AUTOINCREMENT",
		"DROP TABLE IF EXISTS events",
	} {
		if !strings.Contains(sls, want) {
			t.Errorf("0061_events.sqlite.sql: missing %q", want)
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

// TestGuardDown verifies the Story 22.6 rollback safety guard:
// MAKTABA_DISABLE_DOWN refuses `down`/`down-to` for every truthy
// spelling and allows it when unset/falsey.
func TestGuardDown(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "Yes", "on", " true "} {
		t.Setenv("MAKTABA_DISABLE_DOWN", v)
		if err := guardDown(); err == nil {
			t.Errorf("MAKTABA_DISABLE_DOWN=%q: expected rollback refused, got nil", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "off"} {
		t.Setenv("MAKTABA_DISABLE_DOWN", v)
		if err := guardDown(); err != nil {
			t.Errorf("MAKTABA_DISABLE_DOWN=%q: expected rollback allowed, got %v", v, err)
		}
	}
}

// TestRunMigrate_DownGatedByGuard asserts the `down` action is refused
// before any DB connection is attempted when the guard is set — the
// guard fires even with no DATABASE_URL, proving it's a hard gate.
func TestRunMigrate_DownGatedByGuard(t *testing.T) {
	t.Setenv("MAKTABA_DISABLE_DOWN", "1")
	t.Setenv("DATABASE_URL", "")
	dir := repoMigrationsDir(t)
	err := runMigrate([]string{"--dir", dir, "--dsn", "postgres://unused", "down"})
	if err == nil || !strings.Contains(err.Error(), "MAKTABA_DISABLE_DOWN") {
		t.Fatalf("down with guard set: want MAKTABA_DISABLE_DOWN error, got %v", err)
	}
}

// TestRunMigrate_DownToRequiresTarget asserts `down-to` without a
// version argument is a clean usage error (guard unset so we reach the
// arg check).
func TestRunMigrate_DownToRequiresTarget(t *testing.T) {
	t.Setenv("MAKTABA_DISABLE_DOWN", "")
	dir := repoMigrationsDir(t)
	err := runMigrate([]string{"--dir", dir, "--dsn", "postgres://unused", "down-to"})
	if err == nil || !strings.Contains(err.Error(), "requires a target version") {
		t.Fatalf("down-to without target: want usage error, got %v", err)
	}
}
