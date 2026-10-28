package updater

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// migrationsDir writes a migrations folder: its MANIFEST.md rows and its files.
func migrationsDir(t *testing.T, rows []string, files map[string]string, keeps bool) string {
	t.Helper()
	dir := t.TempDir()
	md := "| Slot | Filename | Owning plan | Depends on | Summary |\n|---|---|---|---|---|\n" + strings.Join(rows, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "MANIFEST.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if keeps {
		if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(SchemaKeepFile)), []byte(`{"protocol": 1}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const upDown = "-- +goose Up\n-- +goose StatementBegin\n%s\n-- +goose StatementEnd\n-- +goose Down\nDROP TABLE IF EXISTS gone;\n"

func up(sql string) string { return strings.Replace(upDown, "%s", sql, 1) }

// Only the migrations above the release before's are judged: additive ones (a row, a
// column with a default, an index, a function made again, a trigger dropped and made
// again, a DROP in the Down section or in a comment) keep the schema; a marked row, or a
// drop, rename, retype or tightening in the Up section, puts the dump back.
func TestRollbackSafe(t *testing.T) {
	base := map[string]string{
		"0001_a.sql": up("DROP TABLE old_thing;"), // below the release before: not judged
		"0002_b.sql": up("INSERT INTO job_stages (stage) VALUES ('x') ON CONFLICT DO NOTHING;\nREVOKE INSERT, TRUNCATE ON t FROM r;\n" +
			"CREATE TRIGGER t_trunc AFTER TRUNCATE ON t EXECUTE FUNCTION f();"),
		"0003_c.sql": up("-- drops nothing: DROP TABLE is only said here\nALTER TABLE t ADD COLUMN IF NOT EXISTS c text NOT NULL DEFAULT 'x';\n" +
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS t_c ON t (c);"),
		"0004_d.sql": up("DROP TRIGGER IF EXISTS t_notify ON t;\nCREATE TRIGGER t_notify AFTER INSERT ON t FOR EACH ROW EXECUTE FUNCTION f();\n" +
			"DROP FUNCTION IF EXISTS g(int);\nCREATE OR REPLACE FUNCTION g(int) RETURNS int LANGUAGE sql AS $$ SELECT 1 $$;"),
		"0004_d.sqlite.sql": up("DROP TABLE everything;"), // SQLite's sibling: not this database's
	}
	rows := []string{"| `0001` | `0001_a.sql` | p | — | a |", "| `0002` | `0002_b.sql` | p | — | b |",
		"| `0003` | `0003_c.sql` | p | — | c |", "| `0004` | `0004_d.sql` | p | — | d |"}
	if ok, why := RollbackSafe(migrationsDir(t, rows, base, false), 1, 4); !ok {
		t.Fatalf("additive migrations: %s", why)
	}
	for name, c := range map[string]struct {
		row, file, sql, says string
	}{
		"marked":       {row: "| `0005` | `0005_e.sql` | p | — | e. **Breaking for the previous release**: drops x. |", sql: "SELECT 1;", says: "Breaking for the previous release"},
		"older nodes":  {row: "| `0005` | `0005_e.sql` | p | — | e. **Breaking for older nodes**. |", sql: "SELECT 1;", says: "Breaking for older nodes"},
		"drop column":  {sql: "ALTER TABLE t DROP COLUMN IF EXISTS c;", says: "DROP COLUMN"},
		"truncate":     {sql: "CREATE TRIGGER x AFTER TRUNCATE ON t EXECUTE FUNCTION f();\nTRUNCATE TABLE t;", says: "TRUNCATE"},
		"rename":       {sql: "ALTER TABLE t RENAME COLUMN a TO b;", says: "RENAME"},
		"retype":       {sql: "ALTER TABLE t ALTER COLUMN a TYPE bigint;", says: "TYPE"},
		"tighten":      {sql: "ALTER TABLE t ALTER COLUMN a SET NOT NULL;", says: "SET NOT NULL"},
		"dropped view": {sql: "DROP VIEW IF EXISTS v;", says: "DROP VIEW v"},
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range base {
				files[k] = v
			}
			files["0005_e.sql"] = up(c.sql)
			row := c.row
			if row == "" {
				row = "| `0005` | `0005_e.sql` | p | — | e |"
			}
			ok, why := RollbackSafe(migrationsDir(t, append(append([]string{}, rows...), row), files, false), 1, 5)
			if ok || !strings.Contains(why, c.says) {
				t.Fatalf("= %v, %q (want it to name %q)", ok, why, c.says)
			}
		})
	}
	if ok, _ := RollbackSafe(t.TempDir(), 1, 5); ok {
		t.Fatal("no MANIFEST.md: kept")
	}
}

// The schema is kept only for a release that says it runs on one (its migrations'
// meta/schema-keep.json): a release from before this rule refuses a newer schema, and its
// rollback puts the dump back.
func TestKeepsSchemaFrom(t *testing.T) {
	rows := []string{"| `0001` | `0001_a.sql` | p | — | a |", "| `0002` | `0002_b.sql` | p | — | b |"}
	from := migrationsDir(t, rows[:1], map[string]string{"0001_a.sql": up("SELECT 1;")}, true)
	to := migrationsDir(t, rows, map[string]string{"0001_a.sql": up("SELECT 1;"),
		"0002_b.sql": up("CREATE TABLE IF NOT EXISTS n (id int);")}, true)
	if ok, why := KeepsSchemaFrom(from, to); !ok {
		t.Fatalf("kept = %v (%s)", ok, why)
	}
	old := migrationsDir(t, rows[:1], map[string]string{"0001_a.sql": up("SELECT 1;")}, false)
	if ok, why := KeepsSchemaFrom(old, to); ok || !strings.Contains(why, SchemaKeepFile) {
		t.Fatalf("a release from before the rule: %v (%s)", ok, why)
	}
}

// The repository's own migrations: 0.3.41 to 0.3.42 (slot 0168, the outage's: one row in
// job_stages) and 0169 are rollback-safe; 0128 (breaking for older nodes) is not.
func TestRollbackSafeRepositoryMigrations(t *testing.T) {
	_, self, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(self), "..", "..", "..", "..", "shared", "db", "migrations")
	if ok, why := RollbackSafe(dir, 167, 169); !ok {
		t.Fatalf("0168-0169: %s", why)
	}
	if ok, why := RollbackSafe(dir, 127, 128); ok || !strings.Contains(why, "0128") {
		t.Fatalf("0128 = %v (%s)", ok, why)
	}
	if !keepsNewerSchema(dir) {
		t.Fatalf("this release's migrations do not say it runs on a kept schema (%s)", SchemaKeepFile)
	}
}
