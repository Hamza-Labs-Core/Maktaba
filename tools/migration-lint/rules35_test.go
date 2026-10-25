package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repoMigrations = "../../shared/db/migrations"

// TC5 (story 35.5): every NO TRANSACTION file of the repository is harmless run again
// whole (a crash part way through it: goose marks it unapplied), and the rule catches a
// statement that is not.
func TestNoTransactionFilesIdempotent(t *testing.T) {
	files, err := listMigrations(repoMigrations)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(f, ".sqlite.sql") && reNoTx.Match(body) {
			seen++
		}
		for _, v := range noTxCheck(f, body) {
			t.Errorf("%s: %s", filepath.Base(v.File), v.Message)
		}
	}
	if seen < 10 {
		t.Fatalf("only %d NO TRANSACTION files read: the migrations were not found", seen)
	}

	head := "-- +goose NO TRANSACTION\n-- +goose Up\n"
	for _, c := range []struct {
		sql string
		bad bool
	}{
		{"CREATE INDEX CONCURRENTLY IF NOT EXISTS a ON t (x);", false},
		{"CREATE INDEX CONCURRENTLY a ON t (x);", true},
		{"CREATE TABLE t (id int);", true},
		{"CREATE UNLOGGED TABLE IF NOT EXISTS t (id int);", false},
		{"ALTER TABLE t ADD COLUMN IF NOT EXISTS x int, ALTER COLUMN y SET DEFAULT 0;", false},
		{"ALTER TABLE t ADD COLUMN x int;", true},
		{"ALTER TABLE t RENAME COLUMN x TO y;", true},
		{"ALTER TABLE t ADD CONSTRAINT c CHECK (x > 0);", true},
		{"ALTER TABLE t DROP CONSTRAINT IF EXISTS c;\nALTER TABLE t ADD CONSTRAINT c CHECK (x > 0);", false},
		{"ALTER TABLE t DROP CONSTRAINT IF EXISTS c, ADD CONSTRAINT c CHECK (x IN (1, 2));", false},
		{"INSERT INTO t (x) VALUES (1);", true},
		{"INSERT INTO t (x) VALUES (1) ON CONFLICT DO NOTHING;", false},
		{"INSERT INTO t (x) SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM t);", false},
		{"CREATE FUNCTION f() RETURNS int LANGUAGE sql AS $$ SELECT 1; $$;", true},
		{"CREATE OR REPLACE FUNCTION f() RETURNS int LANGUAGE plpgsql AS $f$ BEGIN RETURN 1; END $f$;", false},
		{"DO $$ BEGIN IF NOT EXISTS (SELECT 1) THEN CREATE TYPE k AS ENUM ('a'); END IF; END $$;", false},
		{"UPDATE t SET x = 1 WHERE x IS NULL; GRANT SELECT ON t TO r; COMMENT ON TABLE t IS 'a;b';", false},
		{"DROP INDEX CONCURRENTLY IF EXISTS a;", false},
		{"DROP TABLE t;", true},
		{"CREATE TYPE k AS ENUM ('a');", true},
	} {
		got := noTxCheck("9999_x.sql", []byte(head+c.sql+"\n-- +goose Down\nDROP TABLE t;\n"))
		if (len(got) > 0) != c.bad {
			t.Errorf("%q: violations %v, want bad=%v", c.sql, got, c.bad)
		}
	}
	// a file in a transaction, a SQLite sibling and the Down section are not this rule's
	if got := noTxCheck("9999_x.sql", []byte("-- +goose Up\nCREATE TABLE t (id int);\n")); len(got) != 0 {
		t.Errorf("a transactional file: %v", got)
	}
	if got := noTxCheck("9999_x.sqlite.sql", []byte(head+"CREATE TABLE t (id int);\n")); len(got) != 0 {
		t.Errorf("a SQLite sibling: %v", got)
	}
}

// TC6 (story 35.5): the MANIFEST's "Breaking for older nodes" rows and the migrations that
// define maktaba_min_node_schema() agree, value for value; a row without the function,
// the function without a row, and another value are each caught.
func TestManifestMarkersMatchFunction(t *testing.T) {
	files, err := listMigrations(repoMigrations)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range manifestCheck(repoMigrations, files) {
		t.Errorf("%s: %s", filepath.Base(v.File), v.Message)
	}

	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fn := func(v string) string {
		return "-- +goose Up\nCREATE OR REPLACE FUNCTION maktaba_min_node_schema() RETURNS integer\n" +
			"    LANGUAGE sql IMMUTABLE AS $$ SELECT " + v + " $$;\n-- +goose Down\nDROP FUNCTION IF EXISTS maktaba_min_node_schema();\n"
	}
	row := func(slot, extra string) string {
		return "| `" + slot + "` | `" + slot + "_x.sql` | plan | - | Something. " + extra + " |\n"
	}
	check := func(want int) {
		t.Helper()
		files, _ := listMigrations(dir)
		got := manifestCheck(dir, files)
		if len(got) != want {
			t.Errorf("violations %v, want %d", got, want)
		}
	}
	write("0200_x.sql", fn("200"))
	write("MANIFEST.md", row("0200", "**Breaking for older nodes**: `maktaba_min_node_schema()` returns 200."))
	check(0)
	write("MANIFEST.md", row("0200", "**Breaking for older nodes**: `maktaba_min_node_schema()` returns 199."))
	check(1) // another value
	write("MANIFEST.md", row("0200", "Additive."))
	check(1) // the function without the marker
	write("MANIFEST.md", row("0200", "**Breaking for older nodes**.")+row("0201", "**Breaking for older nodes**."))
	write("0201_x.sql", "-- +goose Up\nSELECT 1;\n")
	check(1) // a marker without the function (0201); 0200's value is its own slot
}
