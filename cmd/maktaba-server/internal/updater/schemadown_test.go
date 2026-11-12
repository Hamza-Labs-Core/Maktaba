package updater

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Migrations whose Up scripts lose no data (columns, constraints, indexes, tables,
// functions added; a constraint or an index dropped and made again) are undone by their
// Down scripts; one that drops a table or a column, deletes, overwrites or recasts rows,
// or has no Down script, needs the dump.
func TestReversible(t *testing.T) {
	files := map[string]string{
		"0001_a.sql": up("DROP TABLE old_thing;"), // below the release before: not judged
		"0002_b.sql": up("ALTER TABLE p ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'manual';\n" +
			"ALTER TABLE p DROP CONSTRAINT IF EXISTS p_kind_check;\n" +
			"ALTER TABLE p ADD CONSTRAINT p_kind_check CHECK (kind IN ('manual', 'smart'));\n" +
			"-- DELETE FROM p is only said here\nCREATE UNIQUE INDEX IF NOT EXISTS p_u ON p (kind);\nDROP INDEX IF EXISTS p_old;"),
		"0003_c.sql": up("CREATE TABLE IF NOT EXISTS n (id int);\nCREATE OR REPLACE FUNCTION f() RETURNS int LANGUAGE sql AS $$ SELECT 1 $$;\n" +
			"CREATE OR REPLACE FUNCTION touch() RETURNS void LANGUAGE plpgsql AS $f$ BEGIN DELETE FROM d WHERE x; " +
			"UPDATE d SET y = 1; END $f$;"), // a function's DELETE and UPDATE run later, not now
		"0003_c.sqlite.sql": up("DROP TABLE everything;"), // SQLite's sibling: not this database's
	}
	dir := migrationsDir(t, nil, files, false)
	if ok, why := Reversible(dir, 1, 3); !ok {
		t.Fatalf("migrations that lose no data: %s", why)
	}
	for name, c := range map[string]struct{ file, says string }{
		"drop table":  {up("DROP TABLE IF EXISTS n;"), "DROP TABLE"},
		"drop column": {up("ALTER TABLE p DROP COLUMN kind;"), "DROP COLUMN"},
		"bare column": {up("ALTER TABLE p DROP kind;"), "DROP kind"},
		"delete":      {up("DELETE FROM p WHERE kind = 'x';"), "DELETE FROM"},
		"update":      {up("UPDATE p SET kind = 'manual';"), "UPDATE p SET"},
		"truncate":    {up("TRUNCATE p;"), "TRUNCATE"},
		"retype":      {up("ALTER TABLE p ALTER COLUMN kind TYPE int USING 0;"), "TYPE"},
		"no down":     {"-- +goose Up\nALTER TABLE p ADD COLUMN c int;\n", "no Down script"},
	} {
		t.Run(name, func(t *testing.T) {
			f := map[string]string{"0002_b.sql": up("ALTER TABLE p ADD COLUMN c int;"), "0003_c.sql": c.file}
			ok, why := Reversible(migrationsDir(t, nil, f, false), 1, 3)
			if ok || !strings.Contains(why, "0003_c.sql") || !strings.Contains(why, c.says) {
				t.Fatalf("ok %v: %s", ok, why)
			}
		})
	}
	if ok, _ := Reversible(dir, 3, 3); ok {
		t.Fatal("no migration since the release before: nothing for the Down scripts to undo")
	}
}

// The release before's schema is its newest migration; the new release's migrations above
// it are judged.
func TestDownToFrom(t *testing.T) {
	from := migrationsDir(t, nil, map[string]string{"0001_a.sql": up("SELECT 1;")}, false)
	to := migrationsDir(t, nil, map[string]string{"0001_a.sql": up("SELECT 1;"),
		"0002_b.sql": up("ALTER TABLE p ADD COLUMN c int;")}, false)
	if schema, ok, why := DownToFrom(from, to); !ok || schema != 1 {
		t.Fatalf("schema %d, ok %v: %s", schema, ok, why)
	}
	if _, ok, _ := DownToFrom("", to); ok {
		t.Fatal("an unknown release before")
	}
}

// The release that rolled back on 2026-11-07 (0.3.90: slots 0184..0197, its Down scripts
// run on the home by hand that day) is undone by its Down scripts: the real migration
// files, with 0184's and 0189's function bodies that DELETE.
func TestReversibleSmartPlaylists(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(here), "..", "..", "..", "..", "shared", "db", "migrations")
	if ok, why := Reversible(dir, 183, 197); !ok {
		t.Fatalf("0184..0197: %s", why)
	}
}

// Parental controls (Epic 57: slots 0220..0225) are undone by their Down scripts: the
// update rehearsal's waves roll a release with these slots back and want no dump put
// back. Slot 0225 once set app_settings `parental.enabled` with an UPDATE (over slot
// 0221's seed), which no Down script can undo, and every rollback restored the database.
func TestReversibleParentalControls(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(here), "..", "..", "..", "..", "shared", "db", "migrations")
	if ok, why := Reversible(dir, 219, 225); !ok {
		t.Fatalf("0220..0225: %s", why)
	}
}
