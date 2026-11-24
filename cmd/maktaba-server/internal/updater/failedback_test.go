package updater

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The way back from migrations that failed part way: kept when every migration since the
// release before is additive and that release runs on a newer schema; else undone by the
// Down scripts, the one that failed part way by its own; the dump only when a migration
// that ran lost data a Down script cannot give back (lossy), or the way back is not known.
func TestFailedBack(t *testing.T) {
	from := func(keeps bool) string {
		return migrationsDir(t, nil, map[string]string{"0001_a.sql": up("SELECT 1;")}, keeps)
	}
	additive := map[string]string{
		"0001_a.sql": up("SELECT 1;"),
		"0002_b.sql": up("CREATE TABLE IF NOT EXISTS b (id int);"),
		"0003_c.sql": "-- +goose NO TRANSACTION\n" + up("CREATE TABLE IF NOT EXISTS c (id int);\n"+
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS c_id ON c (id);"),
	}
	to := migrationsDir(t, nil, additive, false)

	if how, _, why := FailedBack(from(true), to, 2, 1, false); how != FailedKeep {
		t.Fatalf("additive, the release before keeps: %s (%s)", how, why)
	}
	// the release before cannot run on a newer schema: the Down scripts of 0002, and 0003's own
	how, schema, why := FailedBack(from(false), to, 2, 1, false)
	if how != FailedDown || schema != 1 || !strings.Contains(why, "slot 0003, which failed, is undone by its own Down") {
		t.Fatalf("lossless, no keep: %s %d (%s)", how, schema, why)
	}
	// the first migration failed: nothing ran, only its own part to undo
	if how, schema, _ := FailedBack(from(false), to, 1, 1, false); how != FailedDown || schema != 1 {
		t.Fatalf("the first failed: %s %d", how, schema)
	}
	// every migration ran and the failure came after them: the Down scripts all the same
	if how, schema, _ := FailedBack(from(false), to, 3, 1, false); how != FailedDown || schema != 1 {
		t.Fatalf("all ran: %s %d", how, schema)
	}
	// the version not known: every migration above the slot before is judged, and the Down
	// scripts go back to it (goose knows which ran)
	if how, schema, _ := FailedBack(from(false), to, -1, 1, false); how != FailedDown || schema != 1 {
		t.Fatalf("applied not known: %s %d", how, schema)
	}
	// the slot before the update recorded: it wins over the release before's newest, and the
	// release before's folder is not needed (an install that did not keep it)
	if how, schema, _ := FailedBack("", to, 2, 2, false); how != FailedDown || schema != 2 {
		t.Fatalf("no release before, slot 2 before: %s %d", how, schema)
	}
	// a database that was a newer release's kept schema goes back to the slot it was at
	if how, schema, _ := FailedBack(from(false), to, 2, 2, true); how != FailedDown || schema != 2 {
		t.Fatalf("on a kept schema, slot recorded: %s %d", how, schema)
	}
	// ... and without that slot nobody knows where it was: the dump, said so
	if how, _, why := FailedBack(from(false), to, 2, 0, true); how != FailedRestore || !strings.Contains(why, "kept") {
		t.Fatalf("on a kept schema, slot not recorded: %s (%s)", how, why)
	}
	if how, _, why := FailedBack("", to, 2, 0, false); how != FailedRestore || !strings.Contains(why, "not known") {
		t.Fatalf("nothing known of the schema before: %s (%s)", how, why)
	}
	// below where it was before the update: not one the Down scripts lead from
	if how, _, _ := FailedBack(from(false), to, 0, 1, false); how != FailedRestore {
		t.Fatalf("below the slot before: %s", how)
	}
	for _, applied := range []int64{-1, 0, 3, 9} {
		if how, _, _ := FailedBack(from(true), to, applied, 1, false); how != FailedKeep {
			t.Fatalf("applied %d, keeps: %s", applied, how)
		}
	}
	// a migration that ran lost data its Down script cannot give back: the dump (lossy)
	lossy := map[string]string{"0001_a.sql": up("SELECT 1;"), "0002_b.sql": up("DELETE FROM b WHERE id = 1;"),
		"0003_c.sql": up("CREATE TABLE IF NOT EXISTS c (id int);")}
	if how, _, why := FailedBack(from(false), migrationsDir(t, nil, lossy, false), 2, 1, false); how != FailedLossy ||
		!strings.Contains(why, "0002_b.sql") {
		t.Fatalf("a lossy migration ran: %s (%s)", how, why)
	}
	// the one that failed part way renames (not additive, loses no data): its own Down
	// script renames it back
	renames := map[string]string{"0001_a.sql": up("SELECT 1;"), "0002_b.sql": up("CREATE TABLE IF NOT EXISTS b (id int);"),
		"0003_c.sql": "-- +goose NO TRANSACTION\n" + up("ALTER TABLE a RENAME COLUMN x TO y;")}
	if how, schema, why := FailedBack(from(true), migrationsDir(t, nil, renames, false), 2, 1, false); how != FailedDown ||
		schema != 1 {
		t.Fatalf("a rename failed part way: %s %d (%s)", how, schema, why)
	}
	// the one that failed part way, NO TRANSACTION, overwrote rows: what it overwrote is lost
	// whatever runs after, so the dump; the same in a transaction left nothing
	backfill := "ALTER TABLE b ADD COLUMN IF NOT EXISTS n int;\nUPDATE b SET n = id;\n" +
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS b_n ON b (n);"
	lossyPart := map[string]string{"0001_a.sql": up("SELECT 1;"), "0002_b.sql": up("CREATE TABLE IF NOT EXISTS b (id int);"),
		"0003_c.sql": "-- +goose NO TRANSACTION\n" + up(backfill)}
	if how, _, why := FailedBack(from(false), migrationsDir(t, nil, lossyPart, false), 2, 1, false); how != FailedLossy ||
		!strings.Contains(why, "loses data") {
		t.Fatalf("a NO TRANSACTION backfill failed part way: %s (%s)", how, why)
	}
	// ... unless it says its Down script gives the overwritten values back (lint rule 10)
	said := map[string]string{"0001_a.sql": up("SELECT 1;"), "0002_b.sql": up("CREATE TABLE IF NOT EXISTS b (id int);"),
		"0003_c.sql": "-- +goose NO TRANSACTION\n-- +maktaba down-restores: n is new, the Down drops it\n" + up(backfill)}
	if how, schema, why := FailedBack(from(false), migrationsDir(t, nil, said, false), 2, 1, false); how != FailedDown ||
		schema != 1 {
		t.Fatalf("a backfill whose Down restores: %s %d (%s)", how, schema, why)
	}
	inTx := map[string]string{"0001_a.sql": up("SELECT 1;"), "0002_b.sql": up("CREATE TABLE IF NOT EXISTS b (id int);"),
		"0003_c.sql": up("ALTER TABLE b ADD COLUMN IF NOT EXISTS n int;\nUPDATE b SET n = id;\nALTER TABLE b RENAME n TO m;")}
	if how, schema, why := FailedBack(from(false), migrationsDir(t, nil, inTx, false), 2, 1, false); how != FailedDown || schema != 1 {
		t.Fatalf("a transactional migration failed (it left nothing): %s %d (%s)", how, schema, why)
	}
	// a NO TRANSACTION one with no Down script cannot undo its part
	noDown := map[string]string{"0001_a.sql": up("SELECT 1;"), "0002_b.sql": up("CREATE TABLE IF NOT EXISTS b (id int);"),
		"0003_c.sql": "-- +goose NO TRANSACTION\n-- +goose Up\nCREATE TABLE IF NOT EXISTS c (id int);\n"}
	if how, _, why := FailedBack(from(false), migrationsDir(t, nil, noDown, false), 2, 1, false); how != FailedLossy ||
		!strings.Contains(why, "no Down script") {
		t.Fatalf("no Down script: %s (%s)", how, why)
	}
	// this release's migrations not known
	if how, _, _ := FailedBack(from(false), "", 2, 1, false); how != FailedRestore {
		t.Fatalf("no migrations: %s", how)
	}
}

// The updates of 2026-11-23 (0.3.153 to 0.3.158) and 2026-11-24 (0.3.153 to 0.3.157), on
// their real migration sets: 0.3.153's newest is 0230, and 0.3.157's and 0.3.158's 0231 to
// 0235 are byte for byte this tree's (append-only). Wherever the run stopped, 0231 to 0235,
// the Down scripts take it back to 0230, never the dump: 0235 failed with 0231 to 0234 run,
// as on the owner's home both times. Before #1538 the database step had no Down path (the
// dump, 2 h 45 min, then more than 2 h 18 min); with it, a failure part way through 0232 or
// 0234 still put the dump back (their function writes a DROP INDEX it runs on a build that
// did not finish, taken for a destructive statement).
func TestFailedBackRepositoryMigrations(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	repo := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "shared", "db", "migrations")
	// a release's folder: the repository's migrations up to slot newest, and its MANIFEST
	release := func(newest int64, keeps bool) string {
		dir := t.TempDir()
		ents, err := os.ReadDir(repo)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			n := e.Name()
			s, _, ok := strings.Cut(n, "_")
			v, err := strconv.ParseInt(s, 10, 64)
			if n != "MANIFEST.md" && (!ok || err != nil || v > newest || !strings.HasSuffix(n, ".sql")) {
				continue
			}
			b, err := os.ReadFile(filepath.Join(repo, n))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if keeps {
			_ = os.MkdirAll(filepath.Join(dir, "meta"), 0o755)
			_ = os.WriteFile(filepath.Join(dir, filepath.FromSlash(SchemaKeepFile)), []byte(`{"protocol": 1}`), 0o644)
		}
		return dir
	}
	v153 := release(230, true)               // 0.3.153 runs on a kept schema (protocol 1)
	v157 := release(235, true)               // 0.3.157 and 0.3.158: the same migrations, 0231 to 0235
	for _, before := range []int64{230, 0} { // recorded, or not (the release before's newest)
		for applied := int64(230); applied <= 235; applied++ {
			how, schema, why := FailedBack(v153, v157, applied, before, false)
			if how != FailedDown || schema != 230 {
				t.Errorf("slot before %d, stopped at %04d: %s %d (%s), want the Down scripts back to 0230",
					before, applied, how, schema, why)
			}
		}
	}
	// the decision of 0.3.153's own rollback (schemadown.go): the same Down scripts
	if schema, ok, why := DownToFrom(v153, v157); !ok || schema != 230 {
		t.Fatalf("DownToFrom 0.3.153 -> 0.3.157: %d %v (%s)", schema, ok, why)
	}
}
