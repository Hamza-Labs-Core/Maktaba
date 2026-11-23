package updater

// The way back from an update whose migrations failed part way (the outage of 2026-11-23:
// a CREATE INDEX CONCURRENTLY cancelled by its lock timeout, and the database step put the
// whole 35 GB dump back, 2 h 44 min with the library stopped). Before, only a rollback
// after migrations that all ran could keep the schema (schemakeep.go) or run the Down
// scripts (schemadown.go); a failure part way always put the dump back. The same two rules
// decide it now, on what the failed run left:
//
//   - keep: the release before runs on a newer schema and every migration since its own is
//     rollback-safe. Whatever part of them ran is additive, so it starts on the database as
//     it is; the next try runs what did not finish again (a migration in a transaction
//     left nothing; a NO TRANSACTION one runs again whole, every statement harmless twice:
//     shared/db/migrations/README.md rule 11).
//   - down: the migrations that ran (goose recorded them) lose no data on their way up, so
//     their Down scripts undo them; the one that failed part way left nothing (it ran in a
//     transaction) or only additive work that loses no data (a NO TRANSACTION one whose Up
//     section is rollback-safe and changes no row), which the release before does not see
//     and the next try finishes. The database is then the release before's schema.
//   - restore: anything else, and whatever cannot be told: the dump goes back, as before.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The ways back from a failed update's migrations.
const (
	FailedKeep    = "keep"
	FailedDown    = "down"
	FailedRestore = "restore"
)

// FailedBack decides the way back from an update whose migrations failed: from and to are
// the migrations folders of the release before and of the failed release, applied the
// newest migration the database records (goose's version) after the failure, and onKept
// whether the database before the update was itself a schema kept for the release before
// (a newer release's: its Down scripts are not to's, so it is not undone). schema is the
// slot the Down scripts go back to (FailedDown); why says what decided it.
func FailedBack(from, to string, applied int64, onKept bool) (how string, schema int64, why string) {
	if from == "" || to == "" {
		return FailedRestore, 0, "the releases' migrations are not known"
	}
	fromSchema, toSchema := newestMigration(from), newestMigration(to)
	if fromSchema == 0 || toSchema == 0 {
		return FailedRestore, 0, "the releases' schemas are not known"
	}
	keepWhy := ""
	if !keepsNewerSchema(from) {
		keepWhy = "the release before does not run on a newer schema (it has no " + SchemaKeepFile + ")"
	} else if ok, why := RollbackSafe(to, fromSchema, toSchema); ok {
		return FailedKeep, 0, why
	} else {
		keepWhy = why
	}
	switch {
	case onKept:
		return FailedRestore, 0, keepWhy + "; the database before the update was a newer release's schema, kept"
	case applied < fromSchema || applied >= toSchema:
		return FailedRestore, 0, fmt.Sprintf("%s; the database is at slot %04d, not between %04d and %04d",
			keepWhy, applied, fromSchema, toSchema)
	}
	failed := nextMigration(to, applied)
	if failed == 0 {
		return FailedRestore, 0, keepWhy + "; the migration that failed is not known"
	}
	if why := partWayLeft(to, failed); why != "" {
		return FailedRestore, 0, keepWhy + "; the migration that failed part way: " + why
	}
	if applied > fromSchema {
		if ok, why := Reversible(to, fromSchema, applied); !ok {
			return FailedRestore, 0, keepWhy + "; " + why
		}
	}
	return FailedDown, fromSchema, fmt.Sprintf("%s; the migrations that ran (to slot %04d) lose no data and "+
		"their Down scripts undo them, and slot %04d, which failed, left only additive work", keepWhy, applied, failed)
}

// nextMigration is the first migration of dir above slot (0: none).
func nextMigration(dir string, slot int64) int64 {
	var next int64
	for _, s := range migrationSlots(dir) {
		if s > slot && (next == 0 || s < next) {
			next = s
		}
	}
	return next
}

// migrationSlots are the slots of dir's Postgres migrations.
func migrationSlots(dir string) []int64 {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []int64
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".sqlite.sql") {
			continue
		}
		if s, _, ok := strings.Cut(name, "_"); ok {
			if v, err := strconv.ParseInt(s, 10, 64); err == nil {
				out = append(out, v)
			}
		}
	}
	return out
}

// partWayLeft says why what the migration at slot of dir may have left after failing part
// way is not safe to leave ("" when it is): nothing, when it runs in a transaction (goose
// rolled it back); else its Up section must be rollback-safe and lose no data, as the
// migrations the Down scripts undo must.
func partWayLeft(dir string, slot int64) string {
	name := ""
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err.Error()
	}
	for _, e := range ents {
		n := e.Name()
		if s, _, ok := strings.Cut(n, "_"); ok && strings.HasSuffix(n, ".sql") && !strings.HasSuffix(n, ".sqlite.sql") {
			if v, err := strconv.ParseInt(s, 10, 64); err == nil && v == slot {
				name = n
			}
		}
	}
	if name == "" {
		return fmt.Sprintf("no migration file in slot %04d", slot)
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return err.Error()
	}
	if !noTransaction.Match(b) {
		return "" // its transaction was rolled back: it left nothing
	}
	if ok, why := RollbackSafe(dir, slot-1, slot); !ok {
		return why
	}
	up, _ := sections(string(b))
	if what := losesData(up); what != "" {
		return name + " " + what
	}
	return ""
}

// noTransaction is goose's NO TRANSACTION directive.
var noTransaction = regexp.MustCompile(`(?m)^\s*--\s*\+goose\s+NO\s+TRANSACTION\b`)
