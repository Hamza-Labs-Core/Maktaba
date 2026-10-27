package router_test

// Epic 45 Phase 2 routes against a real migrated Postgres behind the
// production router (slot 0162's tables, ON CONFLICT on playback_state, the
// FOR UPDATE locks, the window function of the playlists' posters): a member
// of library A never reads or changes library B's videos nor another user's
// marks or playlists. Runs when MAKTABA_TEST_PG_URL is set (see
// access_pg_test.go).

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
)

func TestPGBrowsePhase2(t *testing.T) {
	e, count := setupAccess(t) // acLibA holds acVidA1, acLibB acVidB1; member and other see A only
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	a2 := uuid.NewString()
	exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, detected_language, updated_at, created_at)
		VALUES ($1,$2,'/media/ac/a2.mp4','a2.mp4',repeat('a',64),1000,now(),'ready','باب الطهارة',60,'ar',now(),now())`, a2, acLibA)

	// marks: B is 404 and nothing is written; A is marked; the mark is the member's own
	e.want(http.StatusNotFound, "member", "POST", "/api/me/watched", map[string]any{"video_ids": []string{acVidA1, acVidB1}, "watched": true})
	if n := count(`SELECT count(*) FROM playback_state WHERE user_id = $1`, acMember); n != 0 {
		t.Fatalf("a refused mark wrote %d rows", n)
	}
	e.want(http.StatusOK, "member", "POST", "/api/me/watched", map[string]any{"video_ids": []string{acVidA1, a2}, "watched": true})
	e.want(http.StatusOK, "member", "POST", "/api/me/watched", map[string]any{"video_ids": []string{acVidA1}, "watched": true}) // again: the upsert
	if n := count(`SELECT count(*) FROM playback_state WHERE user_id = $1 AND completed`, acMember); n != 2 {
		t.Errorf("member's watched rows %d", n)
	}
	e.want(http.StatusOK, "member", "POST", "/api/me/watched", map[string]any{"video_ids": []string{a2}, "watched": false})
	if n := count(`SELECT count(*) FROM playback_state WHERE user_id = $1 AND completed`, acMember); n != 1 {
		t.Errorf("after unwatched: %d", n)
	}
	if n := count(`SELECT count(*) FROM playback_state WHERE user_id = $1`, acOther); n != 0 {
		t.Errorf("the member's marks wrote the other user's rows")
	}

	// favorites and the list's marks; ?favorite=true is the home's /api/me/videos
	e.want(http.StatusNotFound, "member", "POST", "/api/me/favorites", map[string]any{"video_ids": []string{acVidB1}, "favorite": true})
	e.want(http.StatusOK, "member", "POST", "/api/me/favorites", map[string]any{"video_ids": []string{a2}, "favorite": true})
	list := e.want(http.StatusOK, "member", "GET", "/api/me/videos?favorite=true", nil)
	assertOnlyLib(t, "favorites", list["items"], acLibA, 1)
	list = e.want(http.StatusOK, "other", "GET", "/api/videos?favorite=true", nil)
	assertOnlyLib(t, "other's favorites", list["items"], acLibA, 0)
	list = e.want(http.StatusOK, "member", "GET", "/api/videos?"+url.Values{"letter": {"ب"}}.Encode(), nil)
	assertOnlyLib(t, "letter", list["items"], acLibA, 1)
	if it := list["items"].([]any)[0].(map[string]any); it["favorite"] != true {
		t.Errorf("the list's favorite mark %v", it)
	}
	list = e.want(http.StatusOK, "admin", "GET", "/api/videos?sort=random&limit=10", nil)
	if n := len(list["items"].([]any)); n != 3 || list["next"] != nil {
		t.Errorf("random: %d items, next %v", n, list["next"])
	}
	home := e.want(http.StatusOK, "member", "GET", "/api/home", nil)
	assertOnlyLib(t, "home favorites", home["favorites"], acLibA, 1)
	mine := e.want(http.StatusOK, "member", "GET", "/api/me/videos/"+a2, nil)
	if mine["favorite"] != true || mine["completed"] != false {
		t.Errorf("my a2 %v", mine)
	}
	e.want(http.StatusNotFound, "member", "GET", "/api/me/videos/"+acVidB1, nil)

	// playlists
	pl := e.want(http.StatusCreated, "member", "POST", "/api/me/playlists", map[string]any{"name": "دروس", "video_ids": []string{a2, acVidA1}})
	id, _ := pl["id"].(string)
	if pl["videos"] != float64(2) || len(pl["poster_video_ids"].([]any)) != 2 {
		t.Fatalf("created %v", pl)
	}
	base := "/api/me/playlists/" + id
	for _, who := range []string{"other", "admin"} {
		e.want(http.StatusNotFound, who, "GET", base, nil)
		e.want(http.StatusNotFound, who, "POST", base+"/items", map[string]any{"video_ids": []string{acVidA1}})
		e.want(http.StatusNotFound, who, "DELETE", base, nil)
	}
	e.want(http.StatusNotFound, "member", "POST", base+"/items", map[string]any{"video_ids": []string{acVidB1}})
	doc := e.want(http.StatusOK, "member", "PUT", base+"/order", map[string]any{"video_ids": []string{acVidA1, a2}})
	assertOnlyLib(t, "ordered", doc["items"], acLibA, 2)
	if first := doc["items"].([]any)[0].(map[string]any)["id"]; first != acVidA1 {
		t.Errorf("order: %v first", first)
	}
	// an item of library B (the member lost it): stored, not shown, kept last on a reorder
	exec(`INSERT INTO user_playlist_items (playlist_id, video_id, position) VALUES ($1,$2,0)`, id, acVidB1)
	doc = e.want(http.StatusOK, "member", "GET", base, nil)
	assertOnlyLib(t, "playlist", doc["items"], acLibA, 2)
	e.want(http.StatusOK, "member", "PUT", base+"/order", map[string]any{"video_ids": []string{a2, acVidA1}})
	if n := count(`SELECT position FROM user_playlist_items WHERE playlist_id = $1 AND video_id = $2`, id, acVidB1); n != 2 {
		t.Errorf("the hidden item's place %d", n)
	}
	lists := e.want(http.StatusOK, "member", "GET", "/api/me/playlists", nil)
	if items := lists["items"].([]any); len(items) != 1 || items[0].(map[string]any)["videos"] != float64(2) {
		t.Errorf("playlists %v", lists)
	}
	e.want(http.StatusNoContent, "member", "DELETE", base+"/items/"+a2, nil)
	e.want(http.StatusNoContent, "member", "DELETE", base, nil)
	if n := count(`SELECT count(*) FROM user_playlist_items WHERE playlist_id = $1`, id); n != 0 {
		t.Errorf("items left after delete: %d", n)
	}

	// facets
	f := e.want(http.StatusOK, "member", "GET", "/api/videos/facets?library="+acLibA, nil)
	if l := f["languages"].([]any); len(l) != 1 {
		t.Errorf("languages %v", l)
	}
	e.want(http.StatusNotFound, "member", "GET", "/api/videos/facets?library="+acLibB, nil)

	// on deck (a series of library A: a2 is lesson 1, finished; A1 lesson 2)
	seriesA := uuid.NewString()
	exec(`INSERT INTO series (id, library_id, name) VALUES ($1,$2,'Fiqh')`, seriesA, acLibA)
	exec(`INSERT INTO series_episodes (series_id, video_id, episode) VALUES ($1,$2,1), ($1,$3,2)`, seriesA, a2, acVidA1)
	e.want(http.StatusOK, "member", "POST", "/api/me/watched", map[string]any{"video_ids": []string{a2}, "watched": true})
	e.want(http.StatusOK, "member", "POST", "/api/me/watched", map[string]any{"video_ids": []string{acVidA1}, "watched": false})
	home = e.want(http.StatusOK, "member", "GET", "/api/home", nil)
	assertOnlyLib(t, "on deck", home["on_deck"], acLibA, 1)
	hub := e.want(http.StatusOK, "member", "GET", "/api/libraries/"+acLibA+"/hub", nil)
	assertOnlyLib(t, "hub on deck", hub["rows"].(map[string]any)["on_deck"], acLibA, 1)

	// anonymous
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/me/watched"}, {"POST", "/api/me/favorites"}, {"GET", "/api/me/playlists"},
		{"GET", "/api/videos/facets"}, {"GET", "/api/me/videos/" + a2},
	} {
		if code, _ := e.do("", c.method, c.path, map[string]any{}); code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Errorf("anonymous %s %s: %d", c.method, c.path, code)
		}
	}
}
