package search

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// A fresh library has titles long before transcripts: the title leg must
// find them, filter by library, and treat LIKE metacharacters literally.
func TestRunTitles_MatchesTitlesLiterally(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "titles.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE videos (
		id TEXT PRIMARY KEY, library_id TEXT, title TEXT, filename TEXT, duration_sec REAL,
		detected_language TEXT, deleted_at TEXT, created_at TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]any{
		{"v1", "lib-a", "السيرة النبوية ( 18 )", 3600.0, nil},
		{"v2", "lib-a", "شرح السيرة", nil, nil},
		{"v3", "lib-b", "السيرة في مكة", 60.0, nil},
		{"v4", "lib-a", "السيرة المحذوفة", 60.0, "2026-01-01"},
		{"v5", "lib-a", "100% ready", 60.0, nil},
	} {
		if _, err := db.Exec(`INSERT INTO videos (id, library_id, title, duration_sec, deleted_at)
			VALUES ($1, $2, $3, $4, $5)`, row...); err != nil {
			t.Fatal(err)
		}
	}
	h := &Handler{DB: db}
	ctx := context.Background()

	got, err := h.runTitles(ctx, "السيرة", 10, Filters{LibraryID: []string{"lib-a"}})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, v := range got {
		ids[v.V] = true
	}
	if len(got) != 2 || !ids["v1"] || !ids["v2"] {
		t.Fatalf("want v1, v2 (lib-a, not deleted), got %+v", got)
	}
	// most similar first (SQLite: the shorter title)
	if got[0].V != "v2" || got[0].S <= got[1].S {
		t.Fatalf("want v2 first with the higher score, got %+v", got)
	}

	pct, err := h.runTitles(ctx, "0%", 10, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pct) != 1 || pct[0].V != "v5" {
		t.Fatalf("%% must match literally, got %+v", pct)
	}
	none, err := h.runTitles(ctx, "_", 10, Filters{})
	if err != nil || len(none) != 0 {
		t.Fatalf("_ must match literally, got %+v %v", none, err)
	}
}
