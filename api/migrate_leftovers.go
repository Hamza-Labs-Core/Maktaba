package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pressly/goose/v3"
)

// migrateAppName is the application_name of every session `migrate` opens on Postgres:
// the server's database step finds the migrations' sessions by it, and ends whatever one
// of them has waited on too long (cmd/maktaba-server: pgbundle.MigrateApp, the same name).
const migrateAppName = "maktaba-migrate"

// A CREATE INDEX CONCURRENTLY cancelled part way (its lock timeout, the server stopped)
// leaves its index behind, INVALID: never used to read, still kept up on every write. Its
// migration, which goose did not record, runs again whole at the next try (NO TRANSACTION
// files: shared/db/migrations/README.md rule 11), and its IF NOT EXISTS then took the
// invalid index for done: on 2026-11-23 the second try recorded 0232 (2.99 ms) and the
// third took 0235's first index so, both INVALID. So before goose runs, every index a
// pending migration builds CONCURRENTLY IF NOT EXISTS that is there INVALID, and that no
// session is building now, is dropped: its migration builds it again, valid. Nothing else
// is touched: an invalid index no pending migration names is left as it is.

// concurrentIndex is a CREATE [UNIQUE] INDEX CONCURRENTLY IF NOT EXISTS and its name.
var concurrentIndex = regexp.MustCompile(`(?is)\bCREATE\s+(?:UNIQUE\s+)?INDEX\s+CONCURRENTLY\s+IF\s+NOT\s+EXISTS\s+` +
	`((?:"[^"]+"|[A-Za-z_][A-Za-z0-9_$]*)(?:\.(?:"[^"]+"|[A-Za-z_][A-Za-z0-9_$]*))?)`)

// gooseDown is where a migration's Down section starts.
var gooseDown = regexp.MustCompile(`(?m)^\s*--\s*\+goose\s+Down\b`)

// concurrentIndexesOf names the indexes the Up section of a migration builds
// CONCURRENTLY IF NOT EXISTS (comments left out).
func concurrentIndexesOf(sqlText string) []string {
	up := sqlText
	if loc := gooseDown.FindStringIndex(up); loc != nil {
		up = up[:loc[0]]
	}
	var lines []string
	for _, l := range strings.Split(up, "\n") {
		if i := strings.Index(l, "--"); i >= 0 {
			l = l[:i]
		}
		lines = append(lines, l)
	}
	var names []string
	for _, m := range concurrentIndex.FindAllStringSubmatch(strings.Join(lines, "\n"), -1) {
		names = append(names, m[1])
	}
	return names
}

// invalidLeftoverSQL selects the index named $1 when it is INVALID and no session is
// building it or holds a lock on it (a CREATE INDEX CONCURRENTLY of another session in
// progress is not a leftover), as a name DROP INDEX takes.
const invalidLeftoverSQL = `SELECT c.oid::regclass::text
FROM pg_class c JOIN pg_index i ON i.indexrelid = c.oid
WHERE c.oid = to_regclass($1) AND c.relkind = 'i' AND NOT i.indisvalid
  AND NOT EXISTS (SELECT 1 FROM pg_stat_progress_create_index p WHERE p.index_relid = c.oid)
  AND NOT EXISTS (SELECT 1 FROM pg_locks l WHERE l.relation = c.oid AND l.pid <> pg_backend_pid())`

// dropCancelledIndexes drops the INVALID leftovers of the pending migrations of dir (the
// staged Postgres set) on db, saying each one.
func dropCancelledIndexes(ctx context.Context, db *sql.DB, dir string) error {
	current, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		return fmt.Errorf("the schema's version: %w", err)
	}
	ms, err := goose.CollectMigrations(dir, 0, goose.MaxVersion)
	if err != nil {
		return err
	}
	for _, m := range ms {
		if m.Version <= current {
			continue
		}
		raw, err := os.ReadFile(m.Source)
		if err != nil {
			return err
		}
		for _, name := range concurrentIndexesOf(string(raw)) {
			var idx string
			switch err := db.QueryRowContext(ctx, invalidLeftoverSQL, name).Scan(&idx); {
			case err == sql.ErrNoRows:
				continue
			case err != nil:
				return fmt.Errorf("%s: %w", name, err)
			}
			if _, err := db.ExecContext(ctx, `DROP INDEX CONCURRENTLY IF EXISTS `+idx); err != nil {
				return fmt.Errorf("drop the invalid index %s: %w", idx, err)
			}
			fmt.Fprintf(os.Stderr, "migrate: %s was left INVALID by a CREATE INDEX CONCURRENTLY that did not "+
				"finish: dropped, %s builds it again\n", idx, filepath.Base(m.Source))
		}
	}
	return nil
}
