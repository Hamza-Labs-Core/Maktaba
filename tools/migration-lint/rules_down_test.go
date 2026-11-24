package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rules 9 and 10: every migration of the repository has a Down script, and the rules catch
// a file without one, a NO TRANSACTION Down statement that fails after a partial Up, and
// an Up that loses data and does not say so.
func TestDownScripts(t *testing.T) {
	files, err := listMigrations(repoMigrations)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 200 {
		t.Fatalf("only %d files: the migrations were not found", len(files))
	}
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range downCheck(f, body) {
			t.Errorf("%s: [%s] %s", filepath.Base(v.File), v.Rule, v.Message)
		}
	}

	const noTx = "-- +goose NO TRANSACTION\n"
	for _, c := range []struct {
		name, sql, rule string // rule "": clean
	}{
		{"0240_ok.sql", "-- +goose Up\nCREATE TABLE IF NOT EXISTS t (id int);\n-- +goose Down\nDROP TABLE IF EXISTS t;\n", ""},
		{"0240_no_down.sql", "-- +goose Up\nCREATE TABLE IF NOT EXISTS t (id int);\n", "down-script"},
		{"0240_empty_down.sql", "-- +goose Up\nCREATE TABLE IF NOT EXISTS t (id int);\n-- +goose Down\n-- nothing\n", "down-script"},
		{"0100_old_no_down.sql", "-- +goose Up\nSELECT 1;\n", "down-script"}, // every slot needs one
		// NO TRANSACTION: each Down statement runs whatever part of the Up ran
		{"0240_cic.sql", noTx + "-- +goose Up\nCREATE TABLE IF NOT EXISTS t (id int);\n" +
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS t_id ON t (id);\n-- +goose Down\n" +
			"DROP INDEX CONCURRENTLY IF EXISTS t_id;\nDROP TABLE IF EXISTS t;\n", ""},
		{"0240_rename_back.sql", noTx + "-- +goose Up\nALTER TABLE t RENAME COLUMN a TO b;\n-- +goose Down\n" +
			"ALTER TABLE t RENAME COLUMN b TO a;\n", "down-after-partial-up"},
		{"0240_constraint_back.sql", noTx + "-- +goose Up\nALTER TABLE t DROP CONSTRAINT IF EXISTS c;\n-- +goose Down\n" +
			"ALTER TABLE t ADD CONSTRAINT c CHECK (id > 0);\n", "down-after-partial-up"},
		{"0240_constraint_guarded.sql", noTx + "-- +goose Up\nALTER TABLE t DROP CONSTRAINT IF EXISTS c;\n-- +goose Down\n" +
			"ALTER TABLE t DROP CONSTRAINT IF EXISTS c, ADD CONSTRAINT c CHECK (id > 0);\n", ""},
		{"0240_drop_fn.sql", noTx + "-- +goose Up\nCREATE OR REPLACE FUNCTION f() RETURNS int LANGUAGE sql AS $$ SELECT 1 $$;\n" +
			"-- +goose Down\nDROP FUNCTION f();\n", "down-after-partial-up"},
		{"0200_old_rename_back.sql", noTx + "-- +goose Up\nALTER TABLE t RENAME COLUMN a TO b;\n-- +goose Down\n" +
			"ALTER TABLE t RENAME COLUMN b TO a;\n", ""}, // released before the rule
		// lossless Up
		{"0240_delete.sql", "-- +goose Up\nDELETE FROM t WHERE id = 1;\n-- +goose Down\nSELECT 1;\n", "lossless-up"},
		{"0240_backfill.sql", "-- +goose Up\nALTER TABLE t ADD COLUMN IF NOT EXISTS n int;\nUPDATE t SET n = id;\n" +
			"-- +goose Down\nALTER TABLE t DROP COLUMN IF EXISTS n;\n", "lossless-up"},
		{"0240_backfill_said.sql", "-- +goose Up\n-- +maktaba down-restores: n is new, its Down drops it\n" +
			"ALTER TABLE t ADD COLUMN IF NOT EXISTS n int;\nUPDATE t SET n = id;\n" +
			"-- +goose Down\nALTER TABLE t DROP COLUMN IF EXISTS n;\n", ""},
		{"0240_drop_column.sql", "-- +goose Up\nALTER TABLE t DROP COLUMN IF EXISTS old;\n-- +goose Down\n" +
			"ALTER TABLE t ADD COLUMN IF NOT EXISTS old text;\n", "lossless-up"},
		{"0240_drop_column_said.sql", "-- +goose Up\n-- +maktaba lossy-up: old was never written\n" +
			"ALTER TABLE t DROP COLUMN IF EXISTS old;\n-- +goose Down\nALTER TABLE t ADD COLUMN IF NOT EXISTS old text;\n", ""},
		{"0240_retype.sql", "-- +goose Up\nALTER TABLE t ALTER COLUMN a TYPE bigint;\n-- +goose Down\n" +
			"ALTER TABLE t ALTER COLUMN a TYPE int;\n", "lossless-up"},
		{"0240_drop_table.sql", "-- +goose Up\nDROP TABLE IF EXISTS old;\n-- +goose Down\n" +
			"CREATE TABLE IF NOT EXISTS old (id int);\n", "lossless-up"},
		// a function's body only defines: not a statement that runs now
		{"0240_function.sql", "-- +goose Up\nCREATE OR REPLACE FUNCTION f() RETURNS void LANGUAGE sql AS $$ DELETE FROM t $$;\n" +
			"-- +goose Down\nDROP FUNCTION IF EXISTS f();\n", ""},
		// a DROP in the Down section, or said in a comment, is not the Up's
		{"0240_down_drop.sql", "-- +goose Up\n-- DROP TABLE t is not run here\nCREATE TABLE IF NOT EXISTS t (id int);\n" +
			"-- +goose Down\nDROP TABLE IF EXISTS t;\n", ""},
		{"0200_old_delete.sql", "-- +goose Up\nDELETE FROM t;\n-- +goose Down\nSELECT 1;\n", ""},
		{"0240_x.sqlite.sql", "-- +goose Up\nDELETE FROM t;\n", ""}, // SQLite's sibling: not judged
	} {
		vs := downCheck(c.name, []byte(c.sql))
		var rules []string
		for _, v := range vs {
			rules = append(rules, v.Rule)
		}
		got := strings.Join(rules, ",")
		if c.rule == "" && got != "" || c.rule != "" && !strings.Contains(got, c.rule) {
			t.Errorf("%s: rules %q, want %q", c.name, got, c.rule)
		}
	}
}
