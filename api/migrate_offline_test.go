package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Research item 5 (E3): with the library stopped, an update's index builds run plainly.
// Only a statement that starts CREATE [UNIQUE] INDEX CONCURRENTLY or DROP INDEX
// CONCURRENTLY in the Up section loses its CONCURRENTLY; a function's body, a DO block, a
// string, a comment and the Down section stay as they are.
func TestPlainIndexBuilds(t *testing.T) {
	in := `-- +goose NO TRANSACTION
-- +goose Up
-- built CONCURRENTLY below
-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS a_idx ON a (x);
-- +goose StatementEnd
create unique index
    concurrently if not exists b_uniq on b (y);
DROP INDEX CONCURRENTLY IF EXISTS old_idx;
/* CREATE INDEX CONCURRENTLY in a block comment */
INSERT INTO notes (body) VALUES ('CREATE INDEX CONCURRENTLY x; DROP INDEX CONCURRENTLY y');
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION rebuild() RETURNS void LANGUAGE plpgsql AS $fn$
BEGIN
    EXECUTE 'CREATE INDEX CONCURRENTLY IF NOT EXISTS c_idx ON c (z)';
    DROP INDEX CONCURRENTLY IF EXISTS d_idx;
END
$fn$;
-- +goose StatementEnd
DO $$ BEGIN PERFORM 1; END $$; CREATE INDEX CONCURRENTLY e_idx ON e (w);
REINDEX INDEX CONCURRENTLY a_idx;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS a_idx;
`
	out, n := plainIndexBuilds(in)
	if n != 4 {
		t.Fatalf("%d statements changed, want 4:\n%s", n, out)
	}
	for _, want := range []string{
		"CREATE INDEX IF NOT EXISTS a_idx ON a (x);",
		"create unique index if not exists b_uniq on b (y);",
		"DROP INDEX IF EXISTS old_idx;",
		"/* CREATE INDEX CONCURRENTLY in a block comment */",
		"-- built CONCURRENTLY below",
		"VALUES ('CREATE INDEX CONCURRENTLY x; DROP INDEX CONCURRENTLY y')",
		"EXECUTE 'CREATE INDEX CONCURRENTLY IF NOT EXISTS c_idx ON c (z)';\n    DROP INDEX CONCURRENTLY IF EXISTS d_idx;",
		"DO $$ BEGIN PERFORM 1; END $$; CREATE INDEX e_idx ON e (w);",
		"REINDEX INDEX CONCURRENTLY a_idx;",
		"-- +goose Down\nDROP INDEX CONCURRENTLY IF EXISTS a_idx;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	// a text with nothing to change comes back as it was
	if got, n := plainIndexBuilds("SELECT 1;\n"); n != 0 || got != "SELECT 1;\n" {
		t.Fatalf("%q %d", got, n)
	}
}

// Every migration in the repository: its Up section keeps no top-level CONCURRENTLY build
// once rewritten (a statement start the rewrite missed would wait on old snapshots
// offline), and nothing but those keywords changes.
func TestPlainIndexBuildsOnEveryMigration(t *testing.T) {
	dir := filepath.Join("..", "shared", "db", "migrations")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	topLevel := regexp.MustCompile(`(?im)^\s*(CREATE\s+(UNIQUE\s+)?INDEX|DROP\s+INDEX)\s+CONCURRENTLY\b`)
	total := 0
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".sql" || strings.HasSuffix(name, ".sqlite.sql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		out, n := plainIndexBuilds(string(b))
		total += n
		up := out
		if loc := gooseDown.FindStringIndex(out); loc != nil {
			up = out[:loc[0]]
		}
		if m := topLevel.FindString(up); m != "" && !inDollarQuote(up, m) {
			t.Errorf("%s: %q is left CONCURRENTLY", name, strings.TrimSpace(m))
		}
		removed := len(b) - len(out)
		if removed != n*len(" CONCURRENTLY") && removed != 0 {
			// the keyword may be spelt in another case or spacing: at least the count holds
			if strings.Count(strings.ToUpper(string(b)), "CONCURRENTLY")-strings.Count(strings.ToUpper(out), "CONCURRENTLY") != n {
				t.Errorf("%s: %d bytes removed for %d statements", name, removed, n)
			}
		}
	}
	if total == 0 {
		t.Fatal("no migration builds an index CONCURRENTLY: the test reads the wrong folder")
	}
}

// inDollarQuote reports whether the first match of m in up is inside a $$ body (a
// function's: left as it is on purpose).
func inDollarQuote(up, m string) bool {
	i := strings.Index(up, m)
	return i >= 0 && strings.Count(up[:i], "$$")%2 == 1
}

// The staged folder is rewritten only where something changes.
func TestPlainIndexBuildsIn(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("0001_a.sql", "-- +goose NO TRANSACTION\n-- +goose Up\nCREATE INDEX CONCURRENTLY IF NOT EXISTS i ON t (c);\n")
	write("0002_b.sql", "-- +goose Up\nCREATE TABLE t2 (c int);\n")
	n, err := plainIndexBuildsIn(dir)
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "0001_a.sql"))
	if !strings.Contains(string(b), "CREATE INDEX IF NOT EXISTS i ON t (c);") {
		t.Fatalf("%s", b)
	}
}
