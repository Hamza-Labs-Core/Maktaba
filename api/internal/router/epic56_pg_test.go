package router_test

// Epic 56's server stories against a real migrated Postgres behind the
// production router: smart playlists (56.09 T01-T05), a library's playlists
// (56.10 T01-T02), the Categories tab (56.05 T01-T02), the grid grouped by
// series (56.04 T01-T03), a library's collection mode and promoted rows (56.08
// T01-T03) and the read-only sourced collections (56.06/56.07). Runs when
// MAKTABA_TEST_PG_URL is set (see access_pg_test.go).

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
)

func TestPGEpic56Library(t *testing.T) {
	e, count := setupAccess(t) // acLibA holds acVidA1, acLibB acVidB1; member and other see A only
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	pqSeedVideos(t, e, acLibA, 60) // v0001 … v0060, v0060 the newest
	exec(`UPDATE videos SET detected_language = 'ar' WHERE library_id = $1`, acLibA)
	ids := map[string]string{}
	rows, err := e.db.Query(`SELECT title, id FROM videos WHERE library_id = $1 AND title LIKE 'v%'`, acLibA)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var title, id string
		_ = rows.Scan(&title, &id)
		ids[title] = id
	}
	rows.Close()
	// speaker X speaks in v0001..v0055
	for i := 1; i <= 55; i++ {
		exec(`INSERT INTO speakers (id, video_id, cluster_label, name) VALUES ($1, $2, 'SPEAKER_00', 'X')`, uuid.NewString(), ids[vt(i)])
	}

	// ---- 56.09 smart playlists ----
	smart := e.want(http.StatusCreated, "member", "POST", "/api/me/playlists", map[string]any{
		"name": "X unwatched", "kind": "smart",
		"smart_query": map[string]any{"filters": map[string]string{"watched": "unwatched", "speaker": "X"}, "sort": "created_at", "order": "desc", "limit": 50}})
	sid := smart["id"].(string)
	if smart["kind"] != "smart" || smart["videos"] != float64(50) {
		t.Fatalf("smart created %v", smart)
	}
	doc := e.want(http.StatusOK, "member", "GET", "/api/me/playlists/"+sid, nil)
	items := doc["items"].([]any)
	if len(items) != 50 || items[0].(map[string]any)["id"] != ids["v0055"] {
		t.Fatalf("smart items: %d, first %v", len(items), items[0])
	}
	// T01: one watched leaves, the 51st joins
	e.want(http.StatusOK, "member", "POST", "/api/me/watched", map[string]any{"video_ids": []string{ids["v0055"]}, "watched": true})
	doc = e.want(http.StatusOK, "member", "GET", "/api/me/playlists/"+sid+"/items", nil)
	items = doc["items"].([]any)
	if len(items) != 50 || items[0].(map[string]any)["id"] != ids["v0054"] || items[49].(map[string]any)["id"] != ids["v0005"] {
		t.Errorf("after a watch: %d, first %v", len(items), items[0])
	}
	// T02: item writes, the order and a kind change are 409s
	base := "/api/me/playlists/" + sid
	e.want(http.StatusConflict, "member", "POST", base+"/items", map[string]any{"video_ids": []string{acVidA1}})
	e.want(http.StatusConflict, "member", "DELETE", base+"/items/"+ids["v0054"], nil)
	e.want(http.StatusConflict, "member", "PUT", base+"/order", map[string]any{"video_ids": []string{ids["v0054"]}})
	e.want(http.StatusConflict, "member", "PATCH", base, map[string]any{"kind": "manual"})
	// T03: an unknown key, limit 0 and 6,000, sort=random are 422s naming the field
	for _, q := range []map[string]any{
		{"filters": map[string]string{"colour": "red"}}, {"limit": -1}, {"limit": 6000}, {"sort": "random"},
	} {
		code, out := e.do("member", "POST", "/api/me/playlists", map[string]any{"name": "bad", "kind": "smart", "smart_query": q})
		if code != http.StatusUnprocessableEntity {
			t.Errorf("smart query %v: %d %v", q, code, out)
		}
	}
	// the rules can change
	e.want(http.StatusOK, "member", "PATCH", base, map[string]any{"smart_query": map[string]any{"filters": map[string]string{"speaker": "X"}, "limit": 10}})
	if n := len(e.want(http.StatusOK, "member", "GET", base+"/items", nil)["items"].([]any)); n != 10 {
		t.Errorf("after new rules: %d items", n)
	}
	// T04: another user's is 404 everywhere; no session 401
	for _, who := range []string{"other", "admin"} {
		e.want(http.StatusNotFound, who, "GET", base, nil)
		e.want(http.StatusNotFound, who, "GET", base+"/items", nil)
		e.want(http.StatusNotFound, who, "PATCH", base, map[string]any{"name": "x"})
	}
	if code, _ := e.do("", "GET", base+"/items", nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous items: %d", code)
	}
	// T05: a smart list over every library lists none of a library the owner lost
	all := e.want(http.StatusCreated, "member", "POST", "/api/me/playlists", map[string]any{"name": "everything", "kind": "smart",
		"smart_query": map[string]any{"filters": map[string]string{"language": "ar"}, "limit": 500}})
	if n := len(e.want(http.StatusOK, "member", "GET", "/api/me/playlists/"+all["id"].(string), nil)["items"].([]any)); n != 61 { // A1 too
		t.Errorf("all libraries: %d", n)
	}
	if n := len(e.want(http.StatusOK, "member-lost-a", "GET", "/api/me/playlists/"+all["id"].(string), nil)["items"].([]any)); n != 0 {
		t.Errorf("after losing library A: %d items", n)
	}

	// ---- 56.10 a library's playlists ----
	inA := e.want(http.StatusCreated, "member", "POST", "/api/me/playlists", map[string]any{"name": "A", "video_ids": []string{acVidA1}})
	e.want(http.StatusCreated, "member", "POST", "/api/me/playlists", map[string]any{"name": "empty"})
	onlyB := e.want(http.StatusCreated, "member", "POST", "/api/me/playlists", map[string]any{"name": "onlyB", "kind": "smart",
		"smart_query": map[string]any{"libraries": []string{acLibB}, "filters": map[string]string{"language": "ar"}}})
	e.want(http.StatusCreated, "other", "POST", "/api/me/playlists", map[string]any{"name": "theirs", "video_ids": []string{acVidA1}})
	lists := e.want(http.StatusOK, "member", "GET", "/api/me/playlists?library="+acLibA, nil)["items"].([]any)
	got := map[string]bool{}
	for _, it := range lists {
		got[it.(map[string]any)["name"].(string)] = true
	}
	if len(got) != 3 || !got["A"] || !got["everything"] || !got["X unwatched"] || got["empty"] || got["onlyB"] || got["theirs"] {
		t.Errorf("library A's playlists: %v", got)
	}
	_ = inA
	_ = onlyB
	e.want(http.StatusNotFound, "member", "GET", "/api/me/playlists?library="+acLibB, nil)

	// ---- 56.05 categories ----
	tag := uuid.NewString()
	exec(`INSERT INTO tags (id, name, name_norm) VALUES ($1, 'فقه', 'فقه')`, tag)
	exec(`INSERT INTO video_tags (video_id, tag_id) VALUES ($1, $2), ($3, $2)`, ids["v0001"], tag, ids["v0002"])
	cats := e.want(http.StatusOK, "member", "GET", "/api/libraries/"+acLibA+"/categories", nil)
	sp := cats["speakers"].([]any)
	if len(sp) != 1 || sp[0].(map[string]any)["value"] != "X" || sp[0].(map[string]any)["count"] != float64(55) {
		t.Errorf("speakers %v", sp)
	}
	if tp := cats["topics"].([]any); len(tp) != 1 || tp[0].(map[string]any)["value"] != tag || tp[0].(map[string]any)["count"] != float64(2) {
		t.Errorf("topics %v", tp)
	}
	if lg := cats["languages"].([]any); len(lg) != 1 || lg[0].(map[string]any)["count"] != float64(61) {
		t.Errorf("languages %v", lg)
	}
	if ys := cats["years"].([]any); len(ys) == 0 {
		t.Errorf("years %v", ys)
	}
	e.want(http.StatusNotFound, "member", "GET", "/api/libraries/"+acLibB+"/categories", nil)
	if code, _ := e.do("", "GET", "/api/libraries/"+acLibA+"/categories", nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous categories: %d", code)
	}

	// ---- 56.04 the grid by series ----
	// 3 series over v0001..v0009, the rest (and A1) loose
	for s := 0; s < 3; s++ {
		id := uuid.NewString()
		exec(`INSERT INTO series (id, library_id, name) VALUES ($1, $2, $3)`, id, acLibA, "S"+string(rune('a'+s)))
		for ep := 1; ep <= 3; ep++ {
			exec(`INSERT INTO series_episodes (series_id, video_id, season, episode) VALUES ($1, $2, 1, $3)`, id, ids[vt(s*3+ep)], ep)
		}
	}
	grouped := e.want(http.StatusOK, "member", "GET", "/api/videos?"+url.Values{"library": {acLibA}, "group": {"series"}, "sort": {"name"}, "limit": {"200"}}.Encode(), nil)
	gi := grouped["items"].([]any)
	nSeries := 0
	for _, it := range gi {
		if it.(map[string]any)["kind"] == "series" {
			nSeries++
		}
	}
	if nSeries != 3 || len(gi) != 3+51+1 { // 3 series, v0010..v0060, A1
		t.Errorf("group=series: %d cards, %d series", len(gi), nSeries)
	}
	first := gi[0].(map[string]any)
	if first["kind"] != "series" || first["name"] != "Sa" || first["episode_count"] != float64(3) {
		t.Errorf("first series card %v", first)
	}
	// a fully watched series is not in the unwatched view; paging by cursor
	e.want(http.StatusOK, "member", "POST", "/api/me/watched", map[string]any{"video_ids": []string{ids["v0001"], ids["v0002"], ids["v0003"]}, "watched": true})
	unw := e.want(http.StatusOK, "member", "GET", "/api/videos?"+url.Values{"library": {acLibA}, "group": {"series"}, "sort": {"name"}, "watched": {"unwatched"}, "limit": {"2"}}.Encode(), nil)
	if it := unw["items"].([]any); len(it) != 2 || it[0].(map[string]any)["name"] != "Sb" || unw["next"] == nil {
		t.Errorf("unwatched by series: %v", unw)
	}
	next := e.want(http.StatusOK, "member", "GET", "/api/videos?"+url.Values{"library": {acLibA}, "group": {"series"}, "sort": {"name"}, "watched": {"unwatched"}, "limit": {"2"}, "cursor": {unw["next"].(string)}}.Encode(), nil)
	if it := next["items"].([]any); len(it) != 2 {
		t.Errorf("second page %v", next)
	}
	// T03: another library's viewer lists none of it
	if it := e.want(http.StatusOK, "member", "GET", "/api/videos?"+url.Values{"library": {acLibB}, "group": {"series"}}.Encode(), nil)["items"].([]any); len(it) != 0 {
		t.Errorf("library B by series: %v", it)
	}
	e.want(http.StatusBadRequest, "member", "GET", "/api/videos?group=season", nil)

	// ---- 56.08 collection mode, promoted rows, roles ----
	curated := uuid.NewString()
	exec(`INSERT INTO collections (id, library_id, name, hand_ordered) VALUES ($1, $2, 'Five', true)`, curated, acLibA)
	for i := 1; i <= 5; i++ {
		exec(`INSERT INTO collection_items (collection_id, video_id, position) VALUES ($1, $2, $3)`, curated, ids[vt(20+i)], i)
	}
	e.want(http.StatusForbidden, "member", "PATCH", "/api/libraries/"+acLibA, map[string]any{"collection_mode": "hide_items"})
	e.want(http.StatusUnprocessableEntity, "admin", "PATCH", "/api/libraries/"+acLibA, map[string]any{"collection_mode": "sometimes"})
	lib := e.want(http.StatusOK, "admin", "PATCH", "/api/libraries/"+acLibA, map[string]any{"collection_mode": "hide_items"})
	if lib["collection_mode"] != "hide_items" {
		t.Errorf("patched library %v", lib)
	}
	gridQ := url.Values{"library": {acLibA}, "limit": {"200"}}
	grid := e.want(http.StatusOK, "member", "GET", "/api/videos?"+gridQ.Encode(), nil)
	// the fixture's "A picks" (A1) and "Five"
	if cards := grid["collections"].([]any); len(cards) != 2 || cards[1].(map[string]any)["videos"] != float64(5) {
		t.Errorf("hide_items cards %v", grid["collections"])
	}
	if n := len(grid["items"].([]any)); n != 61-5-1 {
		t.Errorf("hide_items items %d, want 55", n)
	}
	gridQ.Set("watched", "unwatched")
	grid = e.want(http.StatusOK, "member", "GET", "/api/videos?"+gridQ.Encode(), nil)
	if grid["collections"] != nil || len(grid["items"].([]any)) != 61-4 { // v0001-v0003 and v0055 watched
		t.Errorf("a filtered grid under hide_items: cards %v, %d items", grid["collections"], len(grid["items"].([]any)))
	}
	// promoted rows: a viewer may not; an admin shows it on the library and Home
	e.want(http.StatusForbidden, "member", "PATCH", "/api/collections/"+curated, map[string]any{"show_on_library": true})
	if code, _ := e.do("", "PATCH", "/api/collections/"+curated, map[string]any{"show_on_library": true}); code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Errorf("anonymous patch: %d", code)
	}
	e.want(http.StatusOK, "admin", "PATCH", "/api/collections/"+curated, map[string]any{"show_on_library": true, "show_on_home": true, "name_override": "Picked"})
	hub := e.want(http.StatusOK, "member", "GET", "/api/libraries/"+acLibA+"/hub", nil)
	if cr := hub["collection_rows"].([]any); len(cr) != 1 || cr[0].(map[string]any)["collection"].(map[string]any)["name"] != "Picked" ||
		len(cr[0].(map[string]any)["items"].([]any)) != 5 {
		t.Errorf("hub collection rows %v", hub["collection_rows"])
	}
	home := e.want(http.StatusOK, "member", "GET", "/api/home", nil)
	if cr := home["collection_rows"].([]any); len(cr) != 1 {
		t.Errorf("home collection rows %v", home["collection_rows"])
	}
	// a sourced collection: its members are its source's; the display choices are an admin's
	yt := uuid.NewString()
	exec(`INSERT INTO collections (id, library_id, name, source_kind, source_ref, hand_ordered) VALUES ($1, $2, 'PL', 'youtube_playlist', 'PLx', true)`, yt, acLibA)
	exec(`INSERT INTO collection_items (collection_id, video_id, position) VALUES ($1, $2, 1), ($1, $3, 2)`, yt, ids["v0030"], ids["v0031"])
	e.want(http.StatusConflict, "admin", "POST", "/api/collections/"+yt+"/items", map[string]any{"video_ids": []string{acVidA1}})
	e.want(http.StatusConflict, "admin", "PATCH", "/api/collections/"+yt, map[string]any{"name": "renamed"})
	e.want(http.StatusOK, "admin", "PATCH", "/api/collections/"+yt, map[string]any{"name_override": "Lessons", "sort_title": "a"})
	cols := e.want(http.StatusOK, "member", "GET", "/api/libraries/"+acLibA+"/collections?kind=youtube", nil)
	if it := cols["items"].([]any); len(it) != 1 || it[0].(map[string]any)["name"] != "Lessons" || it[0].(map[string]any)["source_kind"] != "youtube_playlist" {
		t.Errorf("youtube collections %v", cols["items"])
	}
	dup := e.want(http.StatusCreated, "admin", "POST", "/api/collections/"+yt+"/duplicate", map[string]any{"name": "Mine"})
	if n := count(`SELECT count(*) FROM collection_items WHERE collection_id = $1`, dup["id"]); n != 2 || dup["source_kind"] != nil {
		t.Errorf("duplicate: %d items, %v", n, dup)
	}
	e.want(http.StatusForbidden, "member", "POST", "/api/collections/"+yt+"/duplicate", nil)
	// hidden: left out of the tab for everyone
	e.want(http.StatusOK, "admin", "PATCH", "/api/collections/"+yt, map[string]any{"hidden": true})
	if it := e.want(http.StatusOK, "admin", "GET", "/api/libraries/"+acLibA+"/collections?kind=youtube", nil)["items"].([]any); len(it) != 0 {
		t.Errorf("a hidden collection is listed: %v", it)
	}
	if it := e.want(http.StatusOK, "admin", "GET", "/api/libraries/"+acLibA+"/collections?kind=youtube&hidden=true", nil)["items"].([]any); len(it) != 1 {
		t.Errorf("an admin asking for hidden ones: %v", it)
	}
	// a queue from a sourced collection plays in its order
	q := e.want(http.StatusCreated, "member", "POST", "/api/me/play-queues", map[string]any{"source": map[string]any{"kind": "collection", "id": yt}})
	if w := idsOf(pqWindow(q)); len(w) != 2 || w[0] != ids["v0030"] {
		t.Errorf("collection queue %v", w)
	}
}

func vt(i int) string {
	s := "000" + itoa(i)
	return "v" + s[len(s)-4:]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
