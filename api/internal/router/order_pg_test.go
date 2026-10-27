package router_test

// Every list of videos opens newest-added first, against a real migrated
// Postgres behind the production router: the video list and its cursor, a
// search of the list by relevance, an explicit sort winning, a playlist and a
// collection until someone reorders them (and slot 0163's backfill of the
// ones reordered before it), and a series' episodes, whose lesson order Play
// all and Up next keep. Runs when MAKTABA_TEST_PG_URL is set (see
// access_pg_test.go).

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPGListsOpenNewestAddedFirst(t *testing.T) {
	e, count := setupAccess(t) // acVidA1 (title "t") was added now, in library A
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	now := time.Now().UTC()
	// added 3 h, 1 h, 30 min and 2 h ago; changed in another order, so a list
	// by updated_at is never the newest-added one by accident
	o1, o2, o3, o4 := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, v := range []struct {
		id, title, desc string
		added, changed  time.Duration
	}{
		{o1, "go basics", "", 3 * time.Hour, 0},
		{o2, "learn go", "", time.Hour, 4 * time.Hour},
		{o3, "intro", "all about go", 30 * time.Minute, 5 * time.Hour},
		{o4, "rust", "", 2 * time.Hour, 6 * time.Hour},
	} {
		exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, description, duration_sec, created_at, updated_at)
			VALUES ($1,$2,$3,$4,encode(sha256(convert_to($10,'UTF8')),'hex'),1,$5,'ready',$6,NULLIF($7,''),60,$8,$9)`,
			v.id, acLibA, "/media/ac/"+v.id+".mp4", v.id+".mp4", now, v.title, v.desc, now.Add(-v.added), now.Add(-v.changed), v.id)
	}
	exec(`UPDATE videos SET updated_at = $1 WHERE id = $2`, now.Add(-7*time.Hour), acVidA1)

	walk := func(qs url.Values) []string {
		t.Helper()
		qs.Set("library", acLibA)
		qs.Set("limit", "1")
		var all []string
		for range 10 {
			out := e.want(http.StatusOK, "member", "GET", "/api/videos?"+qs.Encode(), nil)
			all = append(all, itemIDs(out, "id")...)
			next, _ := out["next"].(string)
			if next == "" {
				return all
			}
			qs.Set("cursor", next)
		}
		t.Fatal("pagination did not end")
		return nil
	}
	check := func(name string, got []string, want ...string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}

	// no sort: newest-added first, a page at a time
	check("no sort", walk(url.Values{}), acVidA1, o3, o2, o4, o1)
	// a sort the person picked wins
	check("title", walk(url.Values{"sort": {"title"}, "order": {"asc"}}), o1, o3, o2, o4, acVidA1)
	check("updated_at", walk(url.Values{"sort": {"updated_at"}}), o1, o2, o3, o4, acVidA1)
	// a search: the titles that start with q, those that hold it, the
	// description only; an explicit sort wins there too
	check("q", walk(url.Values{"q": {"go"}}), o1, o2, o3)
	check("q by created_at", walk(url.Values{"q": {"go"}, "sort": {"created_at"}}), o3, o2, o1)

	// a playlist: newest-added first until its owner reorders it
	out := e.want(http.StatusCreated, "member", "POST", "/api/me/playlists", map[string]any{"name": "Picks", "video_ids": []string{o1, o2, o4}})
	pl, _ := out["id"].(string)
	doc := e.want(http.StatusOK, "member", "GET", "/api/me/playlists/"+pl, nil)
	check("playlist", itemIDs(doc, "id"), o2, o4, o1)
	doc = e.want(http.StatusOK, "member", "PUT", "/api/me/playlists/"+pl+"/order", map[string]any{"video_ids": []string{o1, o4, o2}})
	check("reordered playlist", itemIDs(doc, "id"), o1, o4, o2)
	if n := count(`SELECT count(*) FROM user_playlists WHERE id = $1 AND hand_ordered`, pl); n != 1 {
		t.Errorf("a reordered playlist is not hand ordered")
	}

	// a collection: the same
	col := e.want(http.StatusCreated, "admin", "POST", "/api/collections", map[string]any{"library_id": acLibA, "name": "Ordered"})["id"].(string)
	e.want(http.StatusOK, "admin", "POST", "/api/collections/"+col+"/items", map[string]any{"video_ids": []string{o1, o2, o4}})
	check("collection", itemIDs(e.want(http.StatusOK, "member", "GET", "/api/collections/"+col+"/items", nil), "video_id"), o2, o4, o1)
	e.want(http.StatusOK, "admin", "PATCH", "/api/collections/"+col, map[string]any{"items": []map[string]any{
		{"video_id": o4, "position": 10}, {"video_id": o1, "position": 20}, {"video_id": o2, "position": 30}}})
	check("reordered collection", itemIDs(e.want(http.StatusOK, "member", "GET", "/api/collections/"+col+"/items", nil), "video_id"), o4, o1, o2)

	// slot 0163's backfill: a list whose positions disagree with when its
	// items were added was reordered by hand before the flag existed; one
	// in the order its items came is not
	exec(`UPDATE user_playlists SET hand_ordered = false`)
	exec(`UPDATE collections SET hand_ordered = false`)
	exec(`UPDATE user_playlist_items SET added_at = $1 WHERE playlist_id = $2 AND video_id = $3`, now.Add(-time.Hour), pl, o1)
	exec(`UPDATE user_playlist_items SET added_at = $1 WHERE playlist_id = $2 AND video_id <> $3`, now.Add(-2*time.Hour), pl, o1)
	exec(`UPDATE collection_items SET added_at = $1 WHERE collection_id = $2`, now, col)
	exec(`UPDATE collection_items SET added_at = $1 WHERE collection_id = $2 AND video_id = $3`, now.Add(-time.Hour), acColA, acVidA1)
	up, err := os.ReadFile(filepath.Join("..", "..", "..", "shared", "db", "migrations", "0163_hand_ordered_lists.sql"))
	if err != nil {
		t.Fatal(err)
	}
	exec(strings.Split(string(up), "-- +goose Down")[0])
	if n := count(`SELECT count(*) FROM user_playlists WHERE id = $1 AND hand_ordered`, pl); n != 1 {
		t.Errorf("backfill: the reordered playlist is not hand ordered")
	}
	if n := count(`SELECT count(*) FROM collections WHERE hand_ordered`); n != 0 {
		t.Errorf("backfill: %d collections hand ordered, want none (their items came in their order)", n)
	}

	// a series: its page lists each season newest-added first, ?sort=episode
	// in lesson order; the next episode is the lesson order's either way
	series := uuid.NewString()
	exec(`INSERT INTO series (id, library_id, name) VALUES ($1,$2,'Course')`, series, acLibA)
	exec(`INSERT INTO series_episodes (series_id, video_id, season, episode) VALUES ($1,$2,1,1), ($1,$3,1,2), ($1,$4,1,3)`, series, o1, o4, o2)
	episodes := func(query string) ([]string, string) {
		t.Helper()
		out := e.want(http.StatusOK, "member", "GET", "/api/series/"+series+"/episodes"+query, nil)
		var ids []string
		for _, s := range out["seasons"].([]any) {
			for _, ep := range s.(map[string]any)["episodes"].([]any) {
				ids = append(ids, ep.(map[string]any)["video_id"].(string))
			}
		}
		next, _ := out["next_episode"].(map[string]any)
		id, _ := next["video_id"].(string)
		return ids, id
	}
	got, next := episodes("")
	check("series", got, o2, o4, o1)
	if next != o1 {
		t.Errorf("series: next episode %s, want lesson 1 (%s)", next, o1)
	}
	got, next = episodes("?sort=episode")
	check("series by episode", got, o1, o4, o2)
	if next != o1 {
		t.Errorf("series by episode: next episode %s, want %s", next, o1)
	}
}
