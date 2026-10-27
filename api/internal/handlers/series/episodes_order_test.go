package series

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

const (
	orderLib = "22222222-2222-2222-2222-222222222222"
	viewerID = "33333333-3333-3333-3333-333333333333"
)

// orderSeries is one series on SQLite: season 1's lessons 1, 2 and 3 joined
// the library on days 3, 1 and 2 (newest first: 1, 3, 2), season 2's lesson
// 1 on day 9; the viewer finished season 1's lesson 1.
func orderSeries(t *testing.T) chi.Router {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, q := range []string{
		`CREATE TABLE series (id TEXT PRIMARY KEY, name TEXT, name_override TEXT, poster_path TEXT,
			library_id TEXT, year INTEGER, numbering TEXT NOT NULL DEFAULT 'season', updated_at TEXT)`,
		`CREATE TABLE series_episodes (series_id TEXT, video_id TEXT, season INTEGER, episode INTEGER,
			absolute_number INTEGER, season_override INTEGER, episode_override INTEGER)`,
		`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT, title TEXT, filename TEXT,
			poster_path TEXT, duration_sec REAL, created_at TEXT)`,
		`CREATE TABLE playback_state (user_id TEXT, video_id TEXT, position_sec REAL, completed BOOLEAN)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO series (id, name, library_id) VALUES ($1, 'Fiqh', $2)`, sid, orderLib); err != nil {
		t.Fatal(err)
	}
	for _, e := range []struct {
		id              string
		season, episode int
		day             string
	}{{"s1e1", 1, 1, "03"}, {"s1e2", 1, 2, "01"}, {"s1e3", 1, 3, "02"}, {"s2e1", 2, 1, "09"}} {
		if _, err := db.Exec(`INSERT INTO videos VALUES ($1, $2, $1, $1, NULL, 600, $3)`,
			e.id, orderLib, "2026-10-"+e.day+"T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO series_episodes (series_id, video_id, season, episode) VALUES ($1, $2, $3, $4)`,
			sid, e.id, e.season, e.episode); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO playback_state VALUES ($1, 's1e1', 600, 1)`, viewerID); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	(&Handler{DB: db}).Mount(r)
	return r
}

type episodesBody struct {
	Seasons []struct {
		Season   int       `json:"season"`
		Episodes []Episode `json:"episodes"`
	} `json:"seasons"`
	Sort string   `json:"sort"`
	Next *Episode `json:"next_episode"`
}

func getEpisodes(t *testing.T, r chi.Router, query string) (episodesBody, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	p := admin()
	p.UserID = viewerID
	r.ServeHTTP(rec, authReq(http.MethodGet, "/api/series/"+sid+"/episodes"+query, p))
	var body episodesBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
	}
	return body, rec.Code
}

func seasonIDs(b episodesBody) [][]string {
	out := [][]string{}
	for _, s := range b.Seasons {
		ids := []string{}
		for _, e := range s.Episodes {
			ids = append(ids, e.VideoID)
		}
		out = append(out, ids)
	}
	return out
}

// A series page lists each season's lessons newest-added first, as every
// list of videos; ?sort=episode lists them in lesson order. Either way the
// seasons keep their order and the next episode is the lesson order's (Play
// all, Up next and Continue never follow the display order).
func TestEpisodesNewestAddedFirstWithTheLessonOrderOnRequest(t *testing.T) {
	r := orderSeries(t)
	for _, tc := range []struct {
		query, sort string
		want        [][]string
	}{
		{"", "added", [][]string{{"s1e1", "s1e3", "s1e2"}, {"s2e1"}}},
		{"?sort=added", "added", [][]string{{"s1e1", "s1e3", "s1e2"}, {"s2e1"}}},
		{"?sort=episode", "episode", [][]string{{"s1e1", "s1e2", "s1e3"}, {"s2e1"}}},
	} {
		b, code := getEpisodes(t, r, tc.query)
		if code != http.StatusOK {
			t.Fatalf("%q: status %d", tc.query, code)
		}
		if got := seasonIDs(b); !slices.EqualFunc(got, tc.want, slices.Equal) || b.Sort != tc.sort {
			t.Errorf("%q: seasons %v (sort %q), want %v (%q)", tc.query, got, b.Sort, tc.want, tc.sort)
		}
		if len(b.Seasons) != 2 || b.Seasons[0].Season != 1 || b.Seasons[1].Season != 2 {
			t.Errorf("%q: the seasons out of order: %+v", tc.query, b.Seasons)
		}
		// s1e1 is done: next is lesson 2, though lesson 3 is listed before it
		if b.Next == nil || b.Next.VideoID != "s1e2" {
			t.Errorf("%q: next episode %+v, want s1e2", tc.query, b.Next)
		}
	}
	if _, code := getEpisodes(t, r, "?sort=title"); code != http.StatusBadRequest {
		t.Errorf("sort=title: status %d, want 400", code)
	}
}
