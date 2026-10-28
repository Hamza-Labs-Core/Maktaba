package browse

import (
	"fmt"
	"slices"
	"testing"
)

// The home and a hub read the additions once: Recently Added is the first
// RowSize of them, the same cards in the same order as its own LIMIT query
// gave (videos added at once, ordered by mtime then id), and the grouped
// row is built from the same read.
func TestRecentlyAddedPastOneRow(t *testing.T) {
	e := newEnv(t)
	added := iso(now) // one import: every video shares created_at
	for i := range RowSize + 5 {
		// mtimes out of id order, two tied (the id breaks the tie)
		mtime := fmt.Sprintf("2025-01-%02dT00:00:00.000Z", 1+(i*7)%28)
		if _, err := e.db.Exec(`INSERT INTO videos (id, library_id, title, filename, duration_sec, state, created_at, mtime)
			VALUES ($1,$2,$3,$4,100,'ready',$5,$6)`, v(fmt.Sprintf("c%02d", i)), libC, "C", "c.mp4", added, mtime); err != nil {
			t.Fatal(err)
		}
	}
	want := func(lib string) []string {
		t.Helper()
		var args []any
		cs, err := (&Handler{DB: e.db}).cards(t.Context(), `SELECT `+cardColumns+` FROM videos v WHERE `+
			and(visible, libFilter(scope{all: true}, lib, &args))+`
			ORDER BY v.created_at DESC, v.mtime DESC, v.id DESC LIMIT `+fmt.Sprint(RowSize), args)
		if err != nil {
			t.Fatal(err)
		}
		return ids(cs)
	}

	var home struct {
		Added   []Card       `json:"recently_added"`
		Grouped []AddedEntry `json:"recently_added_grouped"`
	}
	if code := e.do(asAdmin(), "GET", "/api/home", nil, &home); code != 200 {
		t.Fatal(code)
	}
	if got, w := ids(home.Added), want(""); len(got) != RowSize || !slices.Equal(got, w) {
		t.Errorf("home recently added %v, want %v", got, w)
	}
	if len(home.Grouped) != RowSize || home.Grouped[0].Video.ID != home.Added[0].ID {
		t.Errorf("home grouped %d entries, first %v", len(home.Grouped), home.Grouped[0].Video.ID)
	}

	var hub struct {
		Rows struct {
			Added   []Card       `json:"recently_added"`
			Grouped []AddedEntry `json:"recently_added_grouped"`
		} `json:"rows"`
	}
	if code := e.do(asAdmin(), "GET", "/api/libraries/"+libC+"/hub", nil, &hub); code != 200 {
		t.Fatal(code)
	}
	if got, w := ids(hub.Rows.Added), want(libC); len(got) != RowSize || !slices.Equal(got, w) {
		t.Errorf("hub recently added %v, want %v", got, w)
	}
	grouped := make([]string, len(hub.Rows.Grouped))
	for i, g := range hub.Rows.Grouped {
		grouped[i] = g.Video.ID
	}
	if !slices.Equal(grouped, ids(hub.Rows.Added)) {
		t.Errorf("hub grouped %v, want the row's videos (no series)", grouped)
	}
}

// The featured series is the newest with a lesson the caller may watch: a
// newer one whose lessons are all hidden (superseded) is passed over.
func TestFeaturedSkipsASeriesWithNothingToWatch(t *testing.T) {
	e := newEnv(t)
	const hidden = "00000000-0000-4000-8000-0000000005c1"
	for _, q := range []string{
		`UPDATE series SET created_at = CASE id WHEN '` + seriesA + `' THEN '2026-01-01T00:00:00Z' ELSE '2026-06-01T00:00:00Z' END`,
		`INSERT INTO series (id, name, created_at) VALUES ('` + hidden + `', 'Hidden', '2026-09-01T00:00:00Z')`,
		`INSERT INTO series_episodes (series_id, video_id, episode) VALUES ('` + hidden + `', '` + v("a6") + `', 1)`,
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var h struct {
		Featured *Featured `json:"featured"`
	}
	if code := e.do(asAdmin(), "GET", "/api/home", nil, &h); code != 200 {
		t.Fatal(code)
	}
	if f := h.Featured; f == nil || f.Series == nil || f.Series.ID != seriesB || f.Series.Episodes != 1 {
		t.Errorf("admin featured %+v, want B (Hidden has only a superseded lesson)", f)
	}
	h.Featured = nil
	e.do(asViewer(), "GET", "/api/home", nil, &h)
	if f := h.Featured; f == nil || f.Series == nil || f.Series.ID != seriesA || f.Series.Episodes != 2 {
		t.Errorf("viewer featured %+v, want Tafsir with its 2 visible lessons", f)
	}
}
