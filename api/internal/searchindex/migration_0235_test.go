package searchindex

// Slot 0235 (Epic 49, story 49.8): keyword search's terms dictionary, up and down on
// Postgres and on its SQLite twin.

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

func TestSlot0235UpAndDownOnPostgres(t *testing.T) {
	db, _ := clustertest.ScratchDB(t) // migrated to the head: 0235 is up
	up, down := gooseStatements(t, "0235_search_terms.sql")
	excluded := func() []string {
		var s string
		if err := db.QueryRow(`SELECT array_to_string(cluster_copy_excluded(), ',')`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return strings.Split(s, ",")
	}
	have := func() (bool, bool) {
		var table, col bool
		if err := db.QueryRow(`SELECT to_regclass('public.search_terms') IS NOT NULL AND to_regclass('public.search_speakers') IS NOT NULL
			AND to_regclass('public.videos_shown_title_prefix') IS NOT NULL AND to_regclass('public.search_terms_df') IS NOT NULL,
			EXISTS (SELECT FROM information_schema.columns WHERE table_name = 'video_search_docs' AND column_name = 'terms_counted')`).Scan(&table, &col); err != nil {
			t.Fatal(err)
		}
		return table, col
	}
	before := excluded()
	if !slices.Contains(before, "search_terms") || !slices.Contains(before, "search_speakers") || !slices.Contains(before, "video_search_docs") {
		t.Fatalf("cluster_copy_excluded: %v", before)
	}
	// the trgm column spells an Arabic key in ASCII, which pg_trgm reads in any locale
	var lib string
	if err := db.QueryRow(`INSERT INTO libraries (name, roots) VALUES ('l', '{/m}') RETURNING id`).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO search_terms (library_id, term, shown, df) VALUES ($1, 'مدرسه', 'مدرسة', 1), ($1, 'cafe', 'café', 2)`, lib); err != nil {
		t.Fatal(err)
	}
	var trgm string
	var sim float64
	if err := db.QueryRow(`SELECT trgm, similarity(trgm, translate('مدرصه', 'ءابتثجحخدذرزسشصضطظعغفقكلمنهوي', 'abcdefghijklmnopqrstuvwxyz012'))
		FROM search_terms WHERE term = 'مدرسه'`).Scan(&trgm, &sim); err != nil {
		t.Fatal(err)
	}
	if trgm != "yikm0" || sim < 0.3 { // م د ر س ه: y i k m 0
		t.Fatalf("trgm %q, similarity %v", trgm, sim)
	}
	if _, err := db.Exec(`DELETE FROM libraries WHERE id = $1`, lib); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.QueryRow(`SELECT count(*) FROM search_terms`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("a deleted library left %d words (%v)", left, err)
	}

	run(t, db, "down", down)
	if table, col := have(); table || col {
		t.Fatalf("after down: table %v, column %v", table, col)
	}
	after := excluded()
	if slices.Contains(after, "search_terms") || slices.Contains(after, "search_speakers") || len(after) != len(before)-2 {
		t.Fatalf("down's cluster_copy_excluded: %v (before %v)", after, before)
	}
	run(t, db, "up", up)
	run(t, db, "up twice", up)
	if table, col := have(); !table || !col {
		t.Fatalf("after up again: table %v, column %v", table, col)
	}
	if got := excluded(); !slices.Equal(got, before) {
		t.Fatalf("up's cluster_copy_excluded: %v, want %v", got, before)
	}
}

func TestSlot0235OnSQLite(t *testing.T) {
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
	up, down := gooseStatements(t, "0235_search_terms.sqlite.sql")
	var plain []string
	for _, s := range up {
		plain = append(plain, strings.Replace(s, "ADD COLUMN IF NOT EXISTS", "ADD COLUMN", 1))
	}
	run(t, db, "up", plain)
	run(t, db, "rows", []string{
		`INSERT INTO libraries VALUES ('l')`,
		`INSERT INTO search_terms (library_id, term, shown, df) VALUES ('l', 'مدرسه', 'مدرسة', 1)`,
		`INSERT INTO videos VALUES ('v', 'l')`,
		`INSERT INTO video_search_docs (video_id, library_id, body, transcript_id) VALUES ('v', 'l', 'x', 't')`,
	})
	var counted int
	if err := db.QueryRow(`SELECT terms_counted FROM video_search_docs`).Scan(&counted); err != nil || counted != 0 {
		t.Fatalf("terms_counted %d (%v)", counted, err)
	}
	run(t, db, "a library deleted", []string{`DELETE FROM video_search_docs`, `DELETE FROM videos`, `DELETE FROM libraries`})
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM search_terms`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a deleted library left %d words (%v)", n, err)
	}
	run(t, db, "down", down)
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'search_terms'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("after down: %d (%v)", n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('video_search_docs') WHERE name = 'terms_counted'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("after down: terms_counted (%d, %v)", n, err)
	}
}

func TestManifestListsSlot0235(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(migrationsDir(t), "MANIFEST.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "| `0235` | `0235_search_terms.sql` |") {
		t.Fatal("MANIFEST.md has no row for slot 0235")
	}
}
