package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lib/pq"
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
// third took 0235's first index so, both INVALID. So every index a migration builds
// CONCURRENTLY IF NOT EXISTS that is there INVALID, and that no session is building now
// (pg_stat_progress_create_index), is dropped before goose runs: a pending migration
// builds it again; one goose recorded already has it built again here, from its own
// definition. The same runs after goose, so no migration stays recorded with an INVALID
// index (a session that held a lock on the table while the build was cut short, say).
// Nothing else is touched: an invalid index no migration names is left as it is.

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

// invalidIndexesSQL selects, of the index names $1, those there INVALID that no session
// is building now, as a name DROP INDEX takes and their definition.
const invalidIndexesSQL = `SELECT n.name, c.oid::regclass::text, pg_get_indexdef(c.oid)
FROM unnest($1::text[]) AS n(name)
JOIN pg_class c ON c.oid = to_regclass(n.name)
JOIN pg_index i ON i.indexrelid = c.oid
WHERE c.relkind = 'i' AND NOT i.indisvalid
  AND NOT EXISTS (SELECT 1 FROM pg_stat_progress_create_index p WHERE p.index_relid = c.oid)`

// createIndex is the start of pg_get_indexdef's statement.
var createIndex = regexp.MustCompile(`^CREATE (UNIQUE )?INDEX `)

// repairInvalidIndexes drops the INVALID leftovers of the migrations of dir (the staged
// Postgres set) on db and builds again, CONCURRENTLY, those of the migrations goose has
// recorded, saying each one.
func repairInvalidIndexes(ctx context.Context, db *sql.DB, dir string) error {
	current, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		return fmt.Errorf("the schema's version: %w", err)
	}
	ms, err := goose.CollectMigrations(dir, 0, goose.MaxVersion)
	if err != nil {
		return err
	}
	of := map[string]*goose.Migration{}
	var names []string
	for _, m := range ms {
		raw, err := os.ReadFile(m.Source)
		if err != nil {
			return err
		}
		for _, n := range concurrentIndexesOf(string(raw)) {
			if _, ok := of[n]; !ok {
				of[n] = m
				names = append(names, n)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	rows, err := db.QueryContext(ctx, invalidIndexesSQL, pq.Array(names))
	if err != nil {
		return err
	}
	type leftover struct{ name, idx, def string }
	var found []leftover
	for rows.Next() {
		var l leftover
		if err := rows.Scan(&l.name, &l.idx, &l.def); err != nil {
			rows.Close()
			return err
		}
		found = append(found, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, l := range found {
		m := of[l.name]
		if _, err := db.ExecContext(ctx, `DROP INDEX CONCURRENTLY IF EXISTS `+l.idx); err != nil {
			return fmt.Errorf("drop the invalid index %s: %w", l.idx, err)
		}
		if m.Version > current {
			fmt.Fprintf(os.Stderr, "migrate: %s was left INVALID by a CREATE INDEX CONCURRENTLY that did not "+
				"finish: dropped, %s builds it again\n", l.idx, filepath.Base(m.Source))
			continue
		}
		def := createIndex.ReplaceAllString(l.def, "CREATE ${1}INDEX CONCURRENTLY IF NOT EXISTS ")
		if def == l.def {
			return fmt.Errorf("the invalid index %s of %s: its definition is not one to build again: %s", l.idx,
				filepath.Base(m.Source), l.def)
		}
		if _, err := db.ExecContext(ctx, def); err != nil {
			return fmt.Errorf("build the invalid index %s of %s again: %w", l.idx, filepath.Base(m.Source), err)
		}
		fmt.Fprintf(os.Stderr, "migrate: %s of %s (recorded) was INVALID: built again\n", l.idx, filepath.Base(m.Source))
	}
	return nil
}
