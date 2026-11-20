package searchindex

// Slot 0233 (Epic 49, story 49.6): what a keyword search document was built from, up and
// down on Postgres and on its SQLite twin.

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

const slot0233Cols = "doc_version integer NO 0, transcript_revision integer NO 0"

func TestSlot0233UpAndDownOnPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t) // migrated to the head: 0233 is up
	up, down := gooseStatements(t, "0233_video_search_docs_version.sql")
	cols := func() string {
		var s sql.NullString
		if err := db.QueryRow(`SELECT string_agg(column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '-'), ', ' ORDER BY ordinal_position)
			FROM information_schema.columns WHERE table_name = 'video_search_docs' AND column_name IN ('doc_version', 'transcript_revision')`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s.String
	}
	if got := cols(); got != slot0233Cols {
		t.Fatalf("after the migrations: %q, want %q", got, slot0233Cols)
	}
	run(t, db, "down", down)
	if got := cols(); got != "" {
		t.Fatalf("after down: %q", got)
	}
	run(t, db, "up", up)
	run(t, db, "up twice", up)
	if got := cols(); got != slot0233Cols {
		t.Fatalf("after up again: %q", got)
	}
}

func TestSlot0233OnSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "s.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	run(t, db, "videos", []string{
		`CREATE TABLE libraries (id TEXT PRIMARY KEY)`,
		`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE)`,
	})
	up32, _ := gooseStatements(t, "0232_video_search_docs.sqlite.sql")
	run(t, db, "0232", up32)
	up, down := gooseStatements(t, "0233_video_search_docs_version.sqlite.sql")
	// the API's SQLite migrator applies ADD COLUMN IF NOT EXISTS (api/migrate_sqlite.go);
	// stock SQLite takes it without IF NOT EXISTS on a table that lacks the column
	plain := make([]string, len(up))
	for i, s := range up {
		plain[i] = strings.Replace(s, "ADD COLUMN IF NOT EXISTS", "ADD COLUMN", 1)
	}
	run(t, db, "up", plain)
	run(t, db, "a document", []string{
		`INSERT INTO libraries VALUES ('l')`, `INSERT INTO videos VALUES ('v', 'l')`,
		`INSERT INTO video_search_docs (video_id, library_id, body, transcript_id) VALUES ('v', 'l', 'x', 't')`,
	})
	var v, r int
	if err := db.QueryRow(`SELECT doc_version, transcript_revision FROM video_search_docs`).Scan(&v, &r); err != nil || v != 0 || r != 0 {
		t.Fatalf("defaults: %d, %d (%v)", v, r, err)
	}
	run(t, db, "down", down)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('video_search_docs') WHERE name IN ('doc_version', 'transcript_revision')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("after down: %d columns (%v)", n, err)
	}
}

func TestManifestListsSlot0233(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "MANIFEST.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "| `0233` | `0233_video_search_docs_version.sql` |") {
		t.Fatal("MANIFEST.md has no row for slot 0233")
	}
}
