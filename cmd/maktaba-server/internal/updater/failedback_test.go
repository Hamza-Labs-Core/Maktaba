package updater

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The way back from migrations that failed part way (the outage of 2026-11-23): kept when
// every migration since the release before is additive and that release runs on a newer
// schema; else undone by the Down scripts of the ones that ran, when they lose no data and
// the one that failed left only additive work; else the dump.
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

	if how, _, why := FailedBack(from(true), to, 2, false); how != FailedKeep {
		t.Fatalf("additive, the release before keeps: %s (%s)", how, why)
	}
	// the release before cannot run on a newer schema: the Down scripts of 0002, 0003 left additive work
	how, schema, why := FailedBack(from(false), to, 2, false)
	if how != FailedDown || schema != 1 || !strings.Contains(why, "slot 0003, which failed") {
		t.Fatalf("lossless, no keep: %s %d (%s)", how, schema, why)
	}
	// the first migration failed: nothing ran, nothing to undo but its additive part
	if how, schema, _ := FailedBack(from(false), to, 1, false); how != FailedDown || schema != 1 {
		t.Fatalf("the first failed: %s %d", how, schema)
	}
	// a database that was a newer release's kept schema: not this release's Down scripts
	if how, _, why := FailedBack(from(false), to, 2, true); how != FailedRestore || !strings.Contains(why, "kept") {
		t.Fatalf("on a kept schema: %s (%s)", how, why)
	}
	// the version not known, or outside the update's slots: only a keep needs none
	for _, applied := range []int64{-1, 0, 3, 9} {
		if how, _, _ := FailedBack(from(false), to, applied, false); how != FailedRestore {
			t.Fatalf("applied %d: %s", applied, how)
		}
		if how, _, _ := FailedBack(from(true), to, applied, false); how != FailedKeep {
			t.Fatalf("applied %d, keeps: %s", applied, how)
		}
	}
	// a migration that ran lost data: the dump
	lossy := map[string]string{"0001_a.sql": up("SELECT 1;"), "0002_b.sql": up("DELETE FROM b WHERE id = 1;"),
		"0003_c.sql": up("CREATE TABLE IF NOT EXISTS c (id int);")}
	if how, _, why := FailedBack(from(false), migrationsDir(t, nil, lossy, false), 2, false); how != FailedRestore ||
		!strings.Contains(why, "0002_b.sql") {
		t.Fatalf("a lossy migration ran: %s (%s)", how, why)
	}
	// the one that failed part way is destructive (what it did may break the release before)
	bad := map[string]string{"0001_a.sql": up("SELECT 1;"), "0002_b.sql": up("CREATE TABLE IF NOT EXISTS b (id int);"),
		"0003_c.sql": "-- +goose NO TRANSACTION\n" + up("ALTER TABLE a RENAME COLUMN x TO y;")}
	if how, _, why := FailedBack(from(true), migrationsDir(t, nil, bad, false), 2, false); how != FailedRestore ||
		!strings.Contains(why, "failed part way") {
		t.Fatalf("a destructive one failed part way: %s (%s)", how, why)
	}
	// the one that failed part way, NO TRANSACTION, overwrote rows: what it did stays, so
	// the dump; the same in a transaction left nothing, so the Down scripts
	backfill := "ALTER TABLE b ADD COLUMN IF NOT EXISTS n int;\nUPDATE b SET n = id;\n" +
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS b_n ON b (n);"
	lossyPart := map[string]string{"0001_a.sql": up("SELECT 1;"), "0002_b.sql": up("CREATE TABLE IF NOT EXISTS b (id int);"),
		"0003_c.sql": "-- +goose NO TRANSACTION\n" + up(backfill)}
	if how, _, why := FailedBack(from(false), migrationsDir(t, nil, lossyPart, false), 2, false); how != FailedRestore ||
		!strings.Contains(why, "loses data") {
		t.Fatalf("a NO TRANSACTION backfill failed part way: %s (%s)", how, why)
	}
	inTx := map[string]string{"0001_a.sql": up("SELECT 1;"), "0002_b.sql": up("CREATE TABLE IF NOT EXISTS b (id int);"),
		"0003_c.sql": up("ALTER TABLE b ADD COLUMN IF NOT EXISTS n int;\nUPDATE b SET n = id;\nALTER TABLE b RENAME n TO m;")}
	if how, schema, why := FailedBack(from(false), migrationsDir(t, nil, inTx, false), 2, false); how != FailedDown || schema != 1 {
		t.Fatalf("a transactional migration failed (it left nothing): %s %d (%s)", how, schema, why)
	}
	// nothing known
	if how, _, _ := FailedBack("", to, 2, false); how != FailedRestore {
		t.Fatalf("no release before: %s", how)
	}
}

// The update of 2026-11-23 (0.3.153's migrations, up to 0230, to this tree's): with 0231 to
// 0234 run and 0235 failed, as on the owner's home, the Down scripts undo it; the keep rule
// cannot (0232's function writes a DROP INDEX it runs on a build that did not finish).
func TestFailedBackRepositoryMigrations(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	to := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "shared", "db", "migrations")
	from := t.TempDir()
	for _, n := range []string{"MANIFEST.md", "0230_" + slotName(t, to, "0230")} {
		b, err := os.ReadFile(filepath.Join(to, n))
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(from, n), b, 0o644)
	}
	how, schema, why := FailedBack(from, to, 234, false)
	if how != FailedDown || schema != 230 {
		t.Fatalf("0231-0234 run, 0235 failed: %s %d (%s)", how, schema, why)
	}
}

func slotName(t *testing.T, dir, slot string) string {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), slot+"_") && !strings.HasSuffix(e.Name(), ".sqlite.sql") {
			return strings.TrimPrefix(e.Name(), slot+"_")
		}
	}
	t.Fatalf("no %s migration", slot)
	return ""
}
