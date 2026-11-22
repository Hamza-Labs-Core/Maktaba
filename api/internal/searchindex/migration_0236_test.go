package searchindex

// Slot 0236 (Epic 49, story 49.10 follow-up): how a search was asked (search_runs'
// sort_order and asked_filters), up and down on Postgres and on its SQLite twin. A row
// written before it keeps NULL in both.

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

const askedCols = `SELECT count(*) FROM information_schema.columns
	WHERE table_name = 'search_runs' AND column_name IN ('sort_order', 'asked_filters') AND is_nullable = 'YES'`

func TestSlot0236UpAndDownOnPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t) // migrated to the head: 0236 is up
	up, down := gooseStatements(t, "0236_search_runs_asked.sql")
	if n := scalar[int](t, db, askedCols); n != 2 {
		t.Fatalf("at the head: %d nullable columns", n)
	}
	user := scalar[string](t, db, `INSERT INTO users (id, username, pw_hash) VALUES ('00000000-0000-4000-8000-000000000236', 'runs-0236', 'x') RETURNING id::text`)
	insert := `INSERT INTO search_runs (id, user_id, query, mode, created_at_ms, last_used_at_ms, expires_at_ms)
		VALUES ($1, $2, 'q', 'fts', 1, 1, 2)`
	run(t, db, "down", down)
	if n := scalar[int](t, db, askedCols); n != 0 {
		t.Fatalf("after down: %d columns", n)
	}
	// a row written by a release before the slot
	if _, err := db.Exec(insert, "before0236abcdefgh", user); err != nil {
		t.Fatal(err)
	}
	run(t, db, "up", up)
	run(t, db, "up twice", up)
	if n := scalar[int](t, db, askedCols); n != 2 {
		t.Fatalf("after up again: %d columns", n)
	}
	if n := scalar[int](t, db, `SELECT count(*) FROM search_runs WHERE id = 'before0236abcdefgh'
		AND sort_order IS NULL AND asked_filters IS NULL`); n != 1 {
		t.Fatal("the older row is not kept with NULLs")
	}
	// a row the previous release writes on the new schema: it names neither column
	if _, err := db.Exec(insert, "after0236abcdefghi", user); err != nil {
		t.Fatalf("an older release's insert: %v", err)
	}
}

func TestSlot0236OnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	up148, _ := gooseStatements(t, "0148_search_runs.sqlite.sql")
	run(t, db, "0148", up148)
	run(t, db, "an older row", []string{`INSERT INTO search_runs (id, user_id, query, mode, created_at_ms, last_used_at_ms, expires_at_ms)
		VALUES ('r', 'u', 'q', 'fts', 1, 1, 2)`})
	up, down := gooseStatements(t, "0236_search_runs_asked.sqlite.sql")
	var plain []string
	for _, s := range up {
		// the API's SQLite migrator takes ADD COLUMN IF NOT EXISTS; plain SQLite does not
		plain = append(plain, strings.ReplaceAll(s, "ADD COLUMN IF NOT EXISTS", "ADD COLUMN"))
	}
	run(t, db, "up", plain)
	cols := `SELECT count(*) FROM pragma_table_info('search_runs') WHERE name IN ('sort_order', 'asked_filters') AND "notnull" = 0`
	if n := scalar[int](t, db, cols); n != 2 {
		t.Fatalf("after up: %d nullable columns", n)
	}
	if n := scalar[int](t, db, `SELECT count(*) FROM search_runs WHERE sort_order IS NULL AND asked_filters IS NULL`); n != 1 {
		t.Fatal("the older row is not kept with NULLs")
	}
	run(t, db, "down", down)
	if n := scalar[int](t, db, cols); n != 0 {
		t.Fatalf("after down: %d columns", n)
	}
	if n := scalar[int](t, db, `SELECT count(*) FROM search_runs`); n != 1 {
		t.Fatal("down lost the row")
	}
}

func TestManifestListsSlot0236(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "MANIFEST.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "| `0236` | `0236_search_runs_asked.sql` |") {
		t.Fatal("MANIFEST.md has no row for slot 0236")
	}
}
