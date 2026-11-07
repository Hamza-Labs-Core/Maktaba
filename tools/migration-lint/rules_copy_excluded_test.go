package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func copyExcludedMigration(up []string, down []string, extra string) string {
	def := func(names []string) string {
		q := make([]string, len(names))
		for i, n := range names {
			q[i] = "'" + n + "'"
		}
		return "CREATE OR REPLACE FUNCTION cluster_copy_excluded() RETURNS text[]\n" +
			"LANGUAGE sql IMMUTABLE AS $f$\n    SELECT ARRAY[\n        -- secrets\n        " +
			strings.Join(q, ", ") + "\n    ]::text[]\n$f$;\n"
	}
	s := "-- +goose Up\n" + extra + "-- +goose StatementBegin\n" + def(up) + "-- +goose StatementEnd\n"
	s += "-- +goose Down\n"
	if down != nil {
		s += "-- +goose StatementBegin\n" + def(down) + "-- +goose StatementEnd\n"
	}
	return s
}

func checkCopyExcluded(t *testing.T, bodies map[string]string) []violation {
	t.Helper()
	var files []string
	for name := range bodies {
		files = append(files, name)
	}
	sort.Strings(files)
	return copyExcludedCheck(files, func(f string) ([]byte, error) { return []byte(bodies[f]), nil })
}

func TestCopyExcludedKeepsEveryTable(t *testing.T) {
	ok := map[string]string{
		"0203_a.sql": copyExcludedMigration([]string{"secrets", "plugin_catalog"}, []string{"secrets"}, ""),
		"0204_b.sql": copyExcludedMigration([]string{"secrets", "plugin_catalog", "plugin_trust"},
			[]string{"secrets", "plugin_catalog"}, ""),
		"0205_c.sql":        "-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 1;\n",
		"0204_b.sqlite.sql": "-- +goose Up\nSELECT 1;\n",
	}
	if vs := checkCopyExcluded(t, ok); len(vs) != 0 {
		t.Fatalf("a chain that keeps every table: %v", vs)
	}
}

func TestCopyExcludedCatchesAParallelPRsLostTables(t *testing.T) {
	// 0204 was written from 0202's list while 0203 added plugin_catalog
	vs := checkCopyExcluded(t, map[string]string{
		"0202_a.sql": copyExcludedMigration([]string{"secrets"}, nil, ""),
		"0203_b.sql": copyExcludedMigration([]string{"secrets", "plugin_catalog"}, []string{"secrets"}, ""),
		"0204_c.sql": copyExcludedMigration([]string{"secrets", "plugin_trust"}, []string{"secrets"}, ""),
	})
	if len(vs) != 2 {
		t.Fatalf("want the lost table and the Down that does not restore 0203's, got %v", vs)
	}
	if !strings.Contains(vs[0].Message, "loses plugin_catalog, which 0203_b.sql listed") {
		t.Errorf("message: %s", vs[0].Message)
	}
	if !strings.Contains(vs[1].Message, "missing: plugin_catalog") {
		t.Errorf("message: %s", vs[1].Message)
	}
	for _, v := range vs {
		if v.Rule != "copy-excluded-keeps" || filepath.Base(v.File) != "0204_c.sql" {
			t.Errorf("violation: %+v", v)
		}
	}
}

func TestCopyExcludedTakesANamedDrop(t *testing.T) {
	vs := checkCopyExcluded(t, map[string]string{
		"0203_a.sql": copyExcludedMigration([]string{"secrets", "old_table"}, nil, ""),
		"0204_b.sql": copyExcludedMigration([]string{"secrets"}, []string{"secrets", "old_table"},
			"-- cluster_copy_excluded drops: old_table\n"),
	})
	if len(vs) != 0 {
		t.Fatalf("a named drop: %v", vs)
	}
}

func TestCopyExcludedDownMustRestoreThePreviousDefinition(t *testing.T) {
	vs := checkCopyExcluded(t, map[string]string{
		"0203_a.sql": copyExcludedMigration([]string{"secrets", "plugin_catalog"}, nil, ""),
		"0204_b.sql": copyExcludedMigration([]string{"secrets", "plugin_catalog", "plugin_trust"},
			[]string{"secrets", "plugin_catalog", "plugin_trust"}, ""),
	})
	if len(vs) != 1 || !strings.Contains(vs[0].Message, "not there: plugin_trust") {
		t.Fatalf("want the Down that keeps its own table, got %v", vs)
	}
}

// The repo's own history redefines the function many times: every redefinition keeps
// every table, and the rule sees them (a parse that found none would pass vacuously).
func TestCopyExcludedHistoryIsWhole(t *testing.T) {
	dir := filepath.Join("..", "..", "shared", "db", "migrations")
	files, err := listMigrations(dir)
	if err != nil {
		t.Skipf("no migrations folder: %v", err)
	}
	defs := 0
	for _, f := range files {
		if strings.HasSuffix(f, ".sqlite.sql") {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if tables := copyExcludedTables(upSection(body)); tables != nil {
			defs++
			if len(tables) < 10 {
				t.Errorf("%s: only %d tables parsed", filepath.Base(f), len(tables))
			}
		}
	}
	if defs < 10 {
		t.Fatalf("found %d definitions of cluster_copy_excluded(), want the history's", defs)
	}
	if vs := copyExcludedCheck(files, os.ReadFile); len(vs) != 0 {
		t.Fatalf("the history: %v", vs)
	}
}
