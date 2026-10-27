package browse

// Epic 45 Phase 2 routes on SQLite: marks, favorites, a video's own state,
// playlists, facets and On Deck, each with its role rules (a viewer of
// library A never reads or changes anything of library B, nor another user's
// playlist).

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// execMigration runs a migration file's Up section (the real slot's SQL).
func execMigration(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "shared", "db", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	up := string(b)
	if i := strings.Index(up, "-- +goose Down"); i >= 0 {
		up = up[:i]
	}
	if _, err := db.Exec(up); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func asOther() *principal.Principal {
	return &principal.Principal{UserID: other, Libraries: []string{libA}}
}

func adminToken() *principal.Principal {
	return &principal.Principal{UserID: "admin-token", IsAdmin: true, AccessAllLibraries: true}
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestWatchedMarks(t *testing.T) {
	e := newEnv(t)
	mark := func(p *principal.Principal, watched any, vids ...string) int {
		return e.do(p, "POST", "/api/me/watched", map[string]any{"video_ids": vids, "watched": watched}, nil)
	}
	// another library's video, an unknown one, a malformed one: 404, nothing written
	before := e.count(`SELECT COUNT(*) FROM playback_state`)
	for name, vids := range map[string][]string{
		"library B": {v("a3"), v("b1")}, "unknown": {v("zz")}, "malformed": {"x"},
	} {
		if code := mark(asViewer(), true, vids...); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, code)
		}
	}
	if n := e.count(`SELECT COUNT(*) FROM playback_state WHERE completed = 1`); n != 1 || e.count(`SELECT COUNT(*) FROM playback_state`) != before {
		t.Fatalf("a refused request wrote: %d completed", n)
	}
	if code := mark(asViewer(), true); code != http.StatusUnprocessableEntity {
		t.Errorf("no videos: %d", code)
	}
	if code := mark(asViewer(), nil, v("a1")); code != http.StatusUnprocessableEntity {
		t.Errorf("no flag: %d", code)
	}
	var many []string
	for range MaxMarkIDs + 1 {
		many = append(many, v("a1"))
	}
	if code := mark(asViewer(), true, many...); code != http.StatusUnprocessableEntity {
		t.Errorf("501 ids: %d", code)
	}
	if code := mark(adminToken(), true, v("a1")); code != http.StatusForbidden {
		t.Errorf("admin token: %d, want 403", code)
	}
	if code := mark(nil, true, v("a1")); code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d, want 401", code)
	}

	// a1 under way and a6 never played (a hidden one may still be marked)
	var out struct{ Updated int }
	if code := e.do(asViewer(), "POST", "/api/me/watched", map[string]any{"video_ids": []string{v("a1"), v("a3"), v("a1")}, "watched": true}, &out); code != 200 || out.Updated != 2 {
		t.Fatalf("mark watched: %d %+v", code, out)
	}
	if n := e.count(`SELECT COUNT(*) FROM playback_state WHERE user_id = $1 AND video_id IN ($2,$3) AND completed = 1 AND position_sec = 0 AND updated_at = $4`,
		viewer, v("a1"), v("a3"), iso(now)); n != 2 {
		t.Fatalf("watched rows: %d", n)
	}
	// another user's marks are their own
	if code := mark(asOther(), true, v("a2")); code != 200 {
		t.Fatal(code)
	}
	if n := e.count(`SELECT COUNT(*) FROM playback_state WHERE user_id = $1 AND video_id = $2`, other, v("a2")); n != 1 {
		t.Errorf("other's mark: %d", n)
	}

	// unwatched: from the start, not finished, its time kept; a video never played stays without a row
	var at string
	if err := e.db.QueryRow(`SELECT updated_at FROM playback_state WHERE user_id = $1 AND video_id = $2`, viewer, v("a2")).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if code := mark(asViewer(), false, v("a2"), v("a6")); code != 200 {
		t.Fatal(code)
	}
	var pos float64
	var done int
	var at2 string
	if err := e.db.QueryRow(`SELECT position_sec, completed, updated_at FROM playback_state WHERE user_id = $1 AND video_id = $2`, viewer, v("a2")).Scan(&pos, &done, &at2); err != nil {
		t.Fatal(err)
	}
	if pos != 0 || done != 0 || at2 != at {
		t.Errorf("unwatched a2: %v %v %q (was %q)", pos, done, at2, at)
	}
	if n := e.count(`SELECT COUNT(*) FROM playback_state WHERE video_id = $1`, v("a6")); n != 0 {
		t.Errorf("unwatched made a row for a6")
	}
	if n := e.count(`SELECT COUNT(*) FROM playback_state WHERE user_id = $1 AND video_id = $2 AND completed = 1`, other, v("a2")); n != 1 {
		t.Errorf("the viewer's unwatched changed the other user's mark")
	}
}

func TestFavoritesAndMyVideo(t *testing.T) {
	e := newEnv(t)
	fav := func(p *principal.Principal, on bool, vids ...string) int {
		return e.do(p, "POST", "/api/me/favorites", map[string]any{"video_ids": vids, "favorite": on}, nil)
	}
	if code := fav(asViewer(), true, v("a1"), v("b1")); code != http.StatusNotFound {
		t.Errorf("library B favorite: %d", code)
	}
	if e.count(`SELECT COUNT(*) FROM user_favorites`) != 0 {
		t.Fatal("a refused favorite was written")
	}
	if code := fav(adminToken(), true, v("a1")); code != http.StatusForbidden {
		t.Errorf("admin token: %d", code)
	}
	for _, vid := range []string{"a1", "a2", "a1"} {
		if code := fav(asViewer(), true, v(vid)); code != 200 {
			t.Fatal(code)
		}
	}
	if code := fav(asOther(), true, v("a3")); code != 200 {
		t.Fatal(code)
	}
	var h struct {
		Favorites []Card `json:"favorites"`
	}
	e.do(asViewer(), "GET", "/api/home", nil, &h)
	if got := ids(h.Favorites); len(got) != 2 || !slices.Contains(got, v("a1")) || !slices.Contains(got, v("a2")) {
		t.Errorf("home favorites %v: a1 and a2, never the other user's a3", got)
	}
	// a favorite in a library the user lost is not shown
	if _, err := e.db.Exec(`INSERT INTO user_favorites (user_id, video_id, created_at) VALUES ($1,$2,'2099-01-01T00:00:00.000Z')`, viewer, v("b1")); err != nil {
		t.Fatal(err)
	}
	e.do(asViewer(), "GET", "/api/home", nil, &h)
	if slices.Contains(ids(h.Favorites), v("b1")) {
		t.Error("a library B favorite shows to a viewer of A")
	}

	var mv MyVideo
	if code := e.do(asViewer(), "GET", "/api/me/videos/"+v("a1"), nil, &mv); code != 200 {
		t.Fatal(code)
	}
	if !mv.Favorite || mv.PositionSec != 100 || mv.Completed || mv.Series == nil || mv.Series.Name != "Tafsir" || len(mv.Playlists) != 0 {
		t.Errorf("my a1 %+v", mv)
	}
	if code := e.do(asOther(), "GET", "/api/me/videos/"+v("a1"), nil, &mv); code != 200 || mv.Favorite || mv.PositionSec != 0 {
		t.Errorf("the other user sees the viewer's state: %d %+v", code, mv)
	}
	for _, path := range []string{"/api/me/videos/" + v("b1"), "/api/me/videos/" + v("zz"), "/api/me/videos/x"} {
		if code := e.do(asViewer(), "GET", path, nil, nil); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, code)
		}
	}
	if code := fav(asViewer(), false, v("a1")); code != 200 {
		t.Fatal(code)
	}
	if n := e.count(`SELECT COUNT(*) FROM user_favorites WHERE user_id = $1 AND video_id = $2`, viewer, v("a1")); n != 0 {
		t.Error("favorite not removed")
	}
}

type playlistDoc struct {
	Playlist PlaylistSummary `json:"playlist"`
	Items    []Card          `json:"items"`
}

func TestPlaylists(t *testing.T) {
	e := newEnv(t)
	var created PlaylistSummary
	if code := e.do(asViewer(), "POST", "/api/me/playlists", map[string]any{"name": "  Ramadan  ", "video_ids": []string{v("a1"), v("a2")}}, &created); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	// 201 is not decoded by do: read it back
	var list struct{ Items []PlaylistSummary }
	e.do(asViewer(), "GET", "/api/me/playlists", nil, &list)
	if len(list.Items) != 1 || list.Items[0].Name != "Ramadan" || list.Items[0].Videos != 2 || list.Items[0].DurationSec != 1600 ||
		!slices.Equal(list.Items[0].PosterVideoIDs, names("a1", "a2")) {
		t.Fatalf("playlists %+v", list.Items)
	}
	id := list.Items[0].ID
	base := "/api/me/playlists/" + id

	// the rules: another user (and an admin) never sees or changes it
	for _, p := range []*principal.Principal{asOther(), asAdmin()} {
		for _, c := range []struct {
			method, path string
			body         any
		}{
			{"GET", base, nil}, {"PATCH", base, map[string]any{"name": "x"}}, {"DELETE", base, nil},
			{"POST", base + "/items", map[string]any{"video_ids": []string{v("a3")}}},
			{"DELETE", base + "/items/" + v("a1"), nil},
			{"PUT", base + "/order", map[string]any{"video_ids": names("a2", "a1")}},
		} {
			if code := e.do(p, c.method, c.path, c.body, nil); code != http.StatusNotFound {
				t.Errorf("%s %s %s: %d, want 404", p.UserID, c.method, c.path, code)
			}
		}
	}
	if code := e.do(asOther(), "GET", "/api/me/playlists", nil, &list); code != 200 || len(list.Items) != 0 {
		t.Errorf("other's playlists %+v", list.Items)
	}
	if code := e.do(adminToken(), "POST", "/api/me/playlists", map[string]any{"name": "x"}, nil); code != http.StatusForbidden {
		t.Errorf("admin token create: %d", code)
	}
	for name, body := range map[string]map[string]any{
		"no name": {}, "blank": {"name": " "}, "long": {"name": strings.Repeat("ا", MaxPlaylistName+1)},
	} {
		if code := e.do(asViewer(), "POST", "/api/me/playlists", body, nil); code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code := e.do(asViewer(), "POST", "/api/me/playlists", map[string]any{"name": "B", "video_ids": []string{v("b1")}}, nil); code != http.StatusNotFound {
		t.Errorf("create with a library B video: %d", code)
	}
	if code := e.do(asViewer(), "POST", base+"/items", map[string]any{"video_ids": []string{v("a3"), v("b1")}}, nil); code != http.StatusNotFound {
		t.Errorf("add a library B video: %d", code)
	}

	var added struct{ Added int }
	if code := e.do(asViewer(), "POST", base+"/items", map[string]any{"video_ids": []string{v("a3"), v("a1")}}, &added); code != 200 || added.Added != 1 {
		t.Fatalf("add: %d %+v", code, added)
	}
	// an item of a library the viewer lost: stored, never shown or counted
	if _, err := e.db.Exec(`INSERT INTO user_playlist_items (playlist_id, video_id, position, added_at) VALUES ($1,$2,1,'x')`, id, v("b1")); err != nil {
		t.Fatal(err)
	}
	var doc playlistDoc
	e.do(asViewer(), "GET", base, nil, &doc)
	if got := ids(doc.Items); !slices.Equal(got, names("a1", "a2", "a3")) || doc.Playlist.Videos != 3 {
		t.Fatalf("items %v (%d)", got, doc.Playlist.Videos)
	}
	if c := doc.Items[0]; c.PositionSec == nil || *c.PositionSec != 100 {
		t.Errorf("item playback %+v", c)
	}

	// the order: exactly the visible items
	for name, order := range map[string][]string{
		"missing one": names("a3", "a1"), "repeat": names("a3", "a1", "a1"), "hidden one": names("a3", "a1", "a2", "b1"),
	} {
		if code := e.do(asViewer(), "PUT", base+"/order", map[string]any{"video_ids": order}, nil); code != http.StatusUnprocessableEntity {
			t.Errorf("order %s: %d", name, code)
		}
	}
	if code := e.do(asViewer(), "PUT", base+"/order", map[string]any{"video_ids": names("a3", "a1", "a2")}, &doc); code != 200 {
		t.Fatal(code)
	}
	if got := ids(doc.Items); !slices.Equal(got, names("a3", "a1", "a2")) {
		t.Errorf("reordered %v", got)
	}
	var lastPos int
	if err := e.db.QueryRow(`SELECT position FROM user_playlist_items WHERE playlist_id = $1 AND video_id = $2`, id, v("b1")).Scan(&lastPos); err != nil || lastPos != 3 {
		t.Errorf("the hidden item's place: %d %v", lastPos, err)
	}

	// remove: a visible item; a hidden one is 404
	if code := e.do(asViewer(), "DELETE", base+"/items/"+v("b1"), nil, nil); code != http.StatusNotFound {
		t.Errorf("remove a library B item: %d", code)
	}
	if code := e.do(asViewer(), "DELETE", base+"/items/"+v("a1"), nil, nil); code != http.StatusNoContent {
		t.Errorf("remove: %d", code)
	}
	if code := e.do(asViewer(), "DELETE", base+"/items/"+v("a1"), nil, nil); code != http.StatusNotFound {
		t.Errorf("remove again: %d", code)
	}

	var renamed PlaylistSummary
	if code := e.do(asViewer(), "PATCH", base, map[string]any{"name": "رمضان", "description": "دروس"}, &renamed); code != 200 || renamed.Name != "رمضان" || renamed.Description != "دروس" {
		t.Errorf("rename: %d %+v", code, renamed)
	}
	if code := e.do(asViewer(), "DELETE", base, nil, nil); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	if n := e.count(`SELECT COUNT(*) FROM user_playlist_items WHERE playlist_id = $1`, id); n != 0 {
		t.Errorf("items left: %d", n)
	}
	if code := e.do(asViewer(), "GET", base, nil, nil); code != http.StatusNotFound {
		t.Errorf("deleted: %d", code)
	}
	if code := e.do(nil, "GET", "/api/me/playlists", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", code)
	}
}

type facetsBody struct {
	Languages []LanguageFacet `json:"languages"`
	Tags      []TagFacet      `json:"tags"`
	Speakers  []SpeakerFacet  `json:"speakers"`
	Letters   []LetterFacet   `json:"letters"`
}

func TestFacets(t *testing.T) {
	e := newEnv(t)
	for _, q := range []string{
		`UPDATE videos SET detected_language = 'ar' WHERE id IN ('` + v("a1") + `','` + v("a2") + `','` + v("b1") + `')`,
		`UPDATE videos SET detected_language = 'en' WHERE id = '` + v("a3") + `'`,
		`UPDATE videos SET title = 'تفسير' WHERE id = '` + v("a1") + `'`,
		`UPDATE videos SET title = 'alpha' WHERE id = '` + v("a2") + `'`,
		`UPDATE videos SET title = '1 intro' WHERE id = '` + v("a3") + `'`,
		`INSERT INTO tags VALUES ('t1','Fiqh'), ('t2','B only')`,
		`INSERT INTO video_tags VALUES ('` + v("a1") + `','t1'), ('` + v("a4") + `','t1'), ('` + v("b1") + `','t2')`,
		`INSERT INTO speakers VALUES ('s1','` + v("a1") + `','SPEAKER_00','Sheikh A'), ('s2','` + v("a2") + `','SPEAKER_01','SPEAKER_01'),
			('s3','` + v("b1") + `',NULL,'Sheikh B')`,
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var f facetsBody
	if code := e.do(asViewer(), "GET", "/api/videos/facets", nil, &f); code != 200 {
		t.Fatal(code)
	}
	if len(f.Languages) != 2 || f.Languages[0] != (LanguageFacet{"ar", 2}) || f.Languages[1] != (LanguageFacet{"en", 1}) {
		t.Errorf("languages %+v", f.Languages)
	}
	if len(f.Tags) != 1 || f.Tags[0] != (TagFacet{"t1", "Fiqh", 1}) {
		t.Errorf("tags %+v: Fiqh once (a4 is missing), never library B's", f.Tags)
	}
	if len(f.Speakers) != 1 || f.Speakers[0] != (SpeakerFacet{"Sheikh A", 1}) {
		t.Errorf("speakers %+v: the named one only", f.Speakers)
	}
	want := []LetterFacet{{"#", 1}, {"A", 1}, {"ت", 1}}
	if !slices.Equal(f.Letters, want) {
		t.Errorf("letters %+v, want %+v", f.Letters, want)
	}
	if code := e.do(asViewer(), "GET", "/api/videos/facets?library="+libB, nil, nil); code != http.StatusNotFound {
		t.Errorf("library B facets: %d", code)
	}
	if code := e.do(asAdmin(), "GET", "/api/videos/facets?library="+libB, nil, &f); code != 200 || len(f.Speakers) != 1 || f.Speakers[0].Name != "Sheikh B" {
		t.Errorf("admin B facets %d %+v", code, f)
	}
	if code := e.do(nil, "GET", "/api/videos/facets", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", code)
	}
}

func TestOnDeck(t *testing.T) {
	e := newEnv(t)
	// Tafsir: a1 (1, under way), a2 (2, done), a4 (3, missing), a3 (4) -> a3 is next
	if _, err := e.db.Exec(`INSERT INTO series_episodes (series_id, video_id, episode) VALUES ($1,$2,4)`, seriesA, v("a3")); err != nil {
		t.Fatal(err)
	}
	// a3's 10 s is not "under way" (30 s)
	var h struct {
		OnDeck []Card `json:"on_deck"`
	}
	e.do(asViewer(), "GET", "/api/home", nil, &h)
	if got := ids(h.OnDeck); !slices.Equal(got, names("a3")) || h.OnDeck[0].Series == nil || h.OnDeck[0].Series.Name != "Tafsir" {
		t.Fatalf("on deck %+v", h.OnDeck)
	}
	var hub struct {
		Rows struct {
			OnDeck []Card `json:"on_deck"`
		} `json:"rows"`
	}
	e.do(asViewer(), "GET", "/api/libraries/"+libA+"/hub", nil, &hub)
	if got := ids(hub.Rows.OnDeck); !slices.Equal(got, names("a3")) {
		t.Errorf("hub on deck %v", got)
	}
	// under way: Continue Watching has it, On Deck does not
	if _, err := e.db.Exec(`UPDATE playback_state SET position_sec = 200 WHERE user_id = $1 AND video_id = $2`, viewer, v("a3")); err != nil {
		t.Fatal(err)
	}
	e.do(asViewer(), "GET", "/api/home", nil, &h)
	if len(h.OnDeck) != 0 {
		t.Errorf("an under-way lesson on deck: %v", ids(h.OnDeck))
	}
	// the other user finished nothing: no row
	e.do(asOther(), "GET", "/api/home", nil, &h)
	if len(h.OnDeck) != 0 {
		t.Errorf("other's on deck %v", ids(h.OnDeck))
	}
	// library B's series finished by the viewer never shows (b1 is not theirs)
	if _, err := e.db.Exec(`INSERT INTO series_episodes (series_id, video_id, episode) VALUES ($1,$2,2)`, seriesB, v("b2")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE playback_state SET completed = 1 WHERE user_id = $1 AND video_id = $2`, viewer, v("b1")); err != nil {
		t.Fatal(err)
	}
	e.do(asViewer(), "GET", "/api/home", nil, &h)
	for _, c := range h.OnDeck {
		if c.LibraryID != libA {
			t.Errorf("a library B lesson on deck: %+v", c)
		}
	}
	e.do(asAdmin(), "GET", "/api/home", nil, &h)
	if len(h.OnDeck) != 0 {
		t.Errorf("admin has no playback, on deck %v", ids(h.OnDeck))
	}
}
