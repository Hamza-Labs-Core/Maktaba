package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The indexes a migration's Up section builds CONCURRENTLY IF NOT EXISTS: the ones whose
// INVALID leftovers a try again drops first; a Down section's, a comment's and a plain
// CREATE INDEX are not.
func TestConcurrentIndexesOf(t *testing.T) {
	sql := "-- +goose NO TRANSACTION\n-- +goose Up\n-- CREATE INDEX CONCURRENTLY IF NOT EXISTS said_only ON x (a);\n" +
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS search_terms_trgm ON search_terms USING gin (trgm gin_trgm_ops);\n" +
		"create unique index concurrently if not exists Upper_Case ON t (a);\n" +
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS \"Quoted\" ON t (a);\n" +
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS textsearch.idx_s ON t (a);\n" +
		"CREATE INDEX IF NOT EXISTS plain ON t (a);\n" +
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS\n    videos_library_age_tier_idx\n    ON videos (library_id, age_tier);\n" +
		"-- +goose Down\nCREATE INDEX CONCURRENTLY IF NOT EXISTS down_only ON t (a);\n"
	got := concurrentIndexesOf(sql)
	want := []string{"search_terms_trgm", "Upper_Case", `"Quoted"`, "textsearch.idx_s", "videos_library_age_tier_idx"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Every CONCURRENTLY build of the repository's migrations is one the parser finds (a
// spelling it missed would leave that index's INVALID leftover taken for done).
func TestConcurrentIndexesOfRepositoryMigrations(t *testing.T) {
	dir := filepath.Join("..", "shared", "db", "migrations")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".sql") || strings.HasSuffix(e.Name(), ".sqlite.sql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		up := string(b)
		if loc := gooseDown.FindStringIndex(up); loc != nil {
			up = up[:loc[0]]
		}
		var code []string
		for _, l := range strings.Split(up, "\n") {
			if i := strings.Index(l, "--"); i >= 0 {
				l = l[:i]
			}
			code = append(code, l)
		}
		// a statement, not a string a function builds ('CREATE INDEX CONCURRENTLY ... %I')
		stmts := 0
		for _, l := range code {
			if strings.Contains(strings.ToUpper(l), "INDEX CONCURRENTLY IF NOT EXISTS") && !strings.Contains(l, "'") &&
				!strings.Contains(l, "%") {
				stmts++
			}
		}
		if got := len(concurrentIndexesOf(string(b))); got < stmts {
			t.Errorf("%s: %d CONCURRENTLY builds found, %d in its Up section", e.Name(), got, stmts)
		}
		n += stmts
	}
	if n < 20 {
		t.Fatalf("only %d CONCURRENTLY builds in the repository's migrations", n)
	}
}
