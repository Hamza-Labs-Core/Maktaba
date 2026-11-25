package updater

// The way back from an update whose migrations failed part way. A failed update is undone
// by the migrations' own Down scripts (#831). When they fail, or in the one case where
// they cannot give back what an Up script already lost (below), nothing is put back by
// itself: the update is held and a person decides (db_held; the dump is one choice).
//
// The outages of 2026-11-23 (0.3.153 to 0.3.158) and 2026-11-24 (to 0.3.157): 0235's
// CREATE INDEX CONCURRENTLY was cancelled by its lock timeout, and the database step put
// the whole 34 GB dump back, about three hours with the library stopped each time. The
// Down scripts of 0231 to 0234 lose nothing, and 0235's own Down drops what it made part
// way. Before, the database step had no Down path at all (#1538 added one, with the dump
// for any part way failure of a NO TRANSACTION migration that was not additive).
//
//   - keep: the release before runs on a newer schema and every migration since its own is
//     rollback-safe. Whatever part of them ran is additive, so it starts on the database as
//     it is; the next try runs what did not finish again (a migration in a transaction
//     left nothing; a NO TRANSACTION one runs again whole, every statement harmless twice:
//     shared/db/migrations/README.md rule 11).
//   - down: the one that failed is undone by its own Down script (`migrate undo-failed`:
//     goose rolled a transactional one back already), then the ones that ran by theirs
//     (`migrate down-to`), back to the slot the database was at before the update. Every
//     Down script runs after a partial Up, or none (migration-lint's down-script rule, the
//     rehearsal's down-after-partial test).
//   - lossy: a migration that ran deleted, overwrote, recast or dropped data (its Up
//     script: losesData), and its Down script cannot bring that data back. Running the
//     Down scripts would start the release before on a database missing that data, so the
//     update is held, loudly (db_held_lossy): a person decides. migration-lint refuses such an Up script
//     in a new migration unless shared/db/migrations/meta/lints.json exempts it for a time,
//     so a release whose migrations pass the lint never takes this way.
//   - restore: what cannot be told (this release's migrations, or the slot the database was
//     at before the update, not known): held, loudly (db_held_unknown).

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
	FailedLossy   = "lossy"
	FailedRestore = "restore"
)

// FailedBack decides the way back from an update whose migrations failed: from and to are
// the migrations folders of the release before ("" when not kept) and of the failed
// release, applied the newest migration the database records after the failure (goose's
// version; -1 when it cannot be read), before the slot the database was at before the
// update's migrations started (0 when not recorded: a record from before 2026-11-24), and
// onKept whether that database was itself a schema kept for the release before (a newer
// release's). schema is the slot the Down scripts go back to (FailedDown); why says what
// decided it.
func FailedBack(from, to string, applied, before int64, onKept bool) (how string, schema int64, why string) {
	toSchema := int64(0)
	if to != "" {
		toSchema = NewestMigration(to)
	}
	if toSchema == 0 {
		return FailedRestore, 0, "this release's migrations are not known"
	}
	fromSchema := int64(0)
	if from != "" {
		fromSchema = NewestMigration(from)
	}
	keepWhy := ""
	switch {
	case fromSchema == 0:
		keepWhy = "the release before's migrations are not known"
	case !keepsNewerSchema(from):
		keepWhy = "the release before does not run on a newer schema (it has no " + SchemaKeepFile + ")"
	default:
		ok, why := RollbackSafe(to, fromSchema, toSchema)
		if ok {
			return FailedKeep, 0, why
		}
		keepWhy = why
	}
	// the slot the Down scripts go back to: where the database was before the update
	target := before
	switch {
	case target > 0:
	case onKept:
		return FailedRestore, 0, keepWhy + "; the database before the update was a newer release's schema, kept, " +
			"and the slot it was at is not recorded"
	case fromSchema > 0:
		target = fromSchema
	default:
		return FailedRestore, 0, keepWhy + "; the slot the database was at before the update is not known"
	}
	if target > toSchema {
		return FailedRestore, 0, fmt.Sprintf("%s; the database before the update (slot %04d) was ahead of this "+
			"release's migrations (to %04d)", keepWhy, target, toSchema)
	}
	if applied >= 0 && applied < target {
		return FailedRestore, 0, fmt.Sprintf("%s; the database is at slot %04d, below slot %04d where it was before "+
			"the update", keepWhy, applied, target)
	}
	// the migrations that ran: every one above target when that cannot be read
	ran := applied
	if ran < 0 || ran > toSchema {
		ran = toSchema
	}
	if ran > target {
		if ok, why := Reversible(to, target, ran); !ok {
			return FailedLossy, 0, keepWhy + "; " + why
		}
	}
	failed := nextMigration(to, ran)
	if failed != 0 {
		if why := partWayLossy(to, failed); why != "" {
			return FailedLossy, 0, keepWhy + "; the migration that failed part way: " + why
		}
	}
	said := fmt.Sprintf("%s; the migrations that ran (to slot %04d) lose no data and their Down scripts undo them",
		keepWhy, ran)
	if failed != 0 {
		said += fmt.Sprintf(", and slot %04d, which failed, is undone by its own Down script", failed)
	}
	return FailedDown, target, said
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

// partWayLossy says why what the migration at slot of dir did before it failed part way
// cannot be undone by its Down script ("" when it can): in a transaction, goose rolled it
// back; else its Down script must have a statement, and its Up script lose no data (a
// statement that ran before the failure may have deleted or overwritten rows).
func partWayLossy(dir string, slot int64) string {
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
	up, down := sections(string(b))
	if strings.TrimSpace(down) == "" {
		return name + " has no Down script"
	}
	if what := losesData(up); what != "" && !downRestores.Match(b) {
		return name + " " + what
	}
	return ""
}

// noTransaction is goose's NO TRANSACTION directive.
var noTransaction = regexp.MustCompile(`(?m)^\s*--\s*\+goose\s+NO\s+TRANSACTION\b`)
