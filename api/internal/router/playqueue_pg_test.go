package router_test

// Epic 56 story 56.01: the play queue against a real migrated Postgres behind
// the production router (T01-T09 of the story): a shuffled walk through a
// library larger than a chunk never repeats, a seed keeps its order, each kind
// plays in its list route's order, a series starts at its next unwatched
// episode, Play next and Add to queue land where they should, a stale move is
// a 409 with the state, a queue is its owner's alone, an item of a library the
// owner lost is never written or shown, and idle queues are swept. Runs when
// MAKTABA_TEST_PG_URL is set (see access_pg_test.go).

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/playqueue"
)

// pqSeedVideos adds n ready videos to lib, titled "v0000"… (their created_at a
// second apart, the newest last).
func pqSeedVideos(t *testing.T, e *acEnv, lib string, n int) {
	t.Helper()
	if _, err := e.db.Exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, updated_at, created_at)
		SELECT gen_random_uuid(), $1::uuid, '/media/pq/' || $1::text || '/' || i || '.mp4', i || '.mp4',
		       encode(sha256(convert_to($1::text || ':' || i, 'UTF8')), 'hex'), 1000, now(), 'ready',
		       'v' || lpad(i::text, 4, '0'), 60, now(), now() - make_interval(secs => $2::int - i)
		FROM generate_series(1, $2::int) AS i`, lib, n); err != nil {
		t.Fatalf("seed videos: %v", err)
	}
}

// pqItems is a window's items as (pos, id, origin).
type pqItem struct {
	pos    int
	id     string
	origin string
}

func pqWindow(doc map[string]any) []pqItem {
	var out []pqItem
	items, _ := doc["items"].([]any)
	for _, it := range items {
		m := it.(map[string]any)
		out = append(out, pqItem{pos: int(m["pos"].(float64)), id: m["id"].(string), origin: m["origin"].(string)})
	}
	return out
}

// pqCurrent is the window's current item.
func pqCurrent(t *testing.T, doc map[string]any) pqItem {
	t.Helper()
	cur := int(doc["current_pos"].(float64))
	for _, it := range pqWindow(doc) {
		if it.pos == cur {
			return it
		}
	}
	t.Fatalf("the current item %d is not in its window: %v", cur, doc["items"])
	return pqItem{}
}

// pqNext is the item after the current one in the window (ok false at the end).
func pqNext(doc map[string]any) (pqItem, bool) {
	cur := int(doc["current_pos"].(float64))
	for _, it := range pqWindow(doc) {
		if it.pos > cur {
			return it, true
		}
	}
	return pqItem{}, false
}

func pqMove(t *testing.T, e *acEnv, who string, doc map[string]any, pos int) map[string]any {
	t.Helper()
	return e.want(http.StatusOK, who, "PUT", "/api/me/play-queues/"+doc["id"].(string)+"/current",
		map[string]any{"pos": pos, "version": doc["version"]})
}

func TestPGPlayQueue(t *testing.T) {
	e, count := setupAccess(t) // acLibA holds acVidA1, acLibB acVidB1; member and other see A only
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	pqSeedVideos(t, e, acLibA, 1300) // with acVidA1: 1,301 videos, three chunks

	// T01: a shuffled walk through more than two chunks: no repeat, chunks of 500
	q := e.want(http.StatusCreated, "member", "POST", "/api/me/play-queues", map[string]any{
		"source": map[string]any{"kind": "library", "id": acLibA}, "shuffle": true, "device_id": "tv-1"})
	qid := q["id"].(string)
	if n := count(`SELECT count(*) FROM play_queue_items WHERE queue_id = $1`, qid); n != playqueue.ChunkSize {
		t.Fatalf("first chunk %d items, want %d", n, playqueue.ChunkSize)
	}
	if q["shuffle"] != true || q["version"] != float64(1) || q["device_id"] != "tv-1" {
		t.Errorf("created %v", q)
	}
	seen := map[string]bool{pqCurrent(t, q).id: true}
	doc := q
	start := time.Now()
	for moves := 0; ; moves++ {
		nx, ok := pqNext(doc)
		if !ok {
			break
		}
		doc = pqMove(t, e, "member", doc, nx.pos)
		cur := pqCurrent(t, doc)
		if seen[cur.id] {
			t.Fatalf("move %d: %s again", moves, cur.id)
		}
		seen[cur.id] = true
		if held := count(`SELECT count(*) FROM play_queue_items WHERE queue_id = $1`, qid); held > playqueue.MaxHeld {
			t.Fatalf("held %d", held)
		}
	}
	if len(seen) != 1301 {
		t.Fatalf("walked %d videos, want 1301", len(seen))
	}
	if doc["source_done"] != true {
		t.Errorf("the walk's end left the source open: %v", doc["source_done"])
	}
	t.Logf("1,300 moves in %s", time.Since(start))

	// T02: the same seed reads the same order; reshuffle keeps the current item
	shuf := e.want(http.StatusCreated, "member", "POST", "/api/me/play-queues", map[string]any{
		"source": map[string]any{"kind": "library", "id": acLibA}, "shuffle": true})
	sid := shuf["id"].(string)
	w1 := e.want(http.StatusOK, "member", "GET", "/api/me/play-queues/"+sid+"?after=40", nil)
	w2 := e.want(http.StatusOK, "member", "GET", "/api/me/play-queues/"+sid+"?after=40", nil)
	if fmt.Sprint(pqWindow(w1)) != fmt.Sprint(pqWindow(w2)) {
		t.Fatalf("two reads of one queue differ")
	}
	before := pqWindow(w1)
	re := e.want(http.StatusOK, "member", "POST", "/api/me/play-queues/"+sid+"/reshuffle", nil)
	after := pqWindow(re)
	if pqCurrent(t, re).id != before[0].id {
		t.Errorf("reshuffle changed the current item")
	}
	same := 0
	for i := 1; i < len(after) && i < len(before); i++ {
		if after[i].id == before[i].id {
			same++
		}
	}
	if same > 3 {
		t.Errorf("reshuffle kept %d of the next items in place", same)
	}

	// T03: not shuffled, a library plays in its grid's order (filters and sort too)
	grid := e.want(http.StatusOK, "member", "GET", "/api/videos?"+url.Values{"library": {acLibA}, "sort": {"title"}, "order": {"asc"}, "limit": {"31"}}.Encode(), nil)
	inOrder := e.want(http.StatusCreated, "member", "POST", "/api/me/play-queues", map[string]any{
		"source": map[string]any{"kind": "library", "id": acLibA, "filters": map[string]string{"sort": "title", "order": "asc"}}})
	if got, want := fmt.Sprint(idsOf(pqWindow(inOrder))), fmt.Sprint(itemIDs(grid, "id")); got != want {
		t.Errorf("library order\n got %s\nwant %s", got, want)
	}
	// an unknown filter is a 422 naming it; an empty view is empty_source
	if code, out := e.do("member", "POST", "/api/me/play-queues", map[string]any{
		"source": map[string]any{"kind": "library", "id": acLibA, "filters": map[string]string{"colour": "red"}}}); code != http.StatusUnprocessableEntity {
		t.Errorf("unknown filter: %d %v", code, out)
	}
	if code, out := e.do("member", "POST", "/api/me/play-queues", map[string]any{
		"source": map[string]any{"kind": "library", "id": acLibA, "filters": map[string]string{"q": "nothing-matches-this"}}}); code != http.StatusUnprocessableEntity || out["title"] != "empty_source" {
		t.Errorf("empty source: %d %v", code, out)
	}
	// a collection in its order (colA holds A1 and B1: B1 is not the member's)
	col := e.want(http.StatusCreated, "member", "POST", "/api/me/play-queues", map[string]any{
		"source": map[string]any{"kind": "collection", "id": acColA}})
	if ids := idsOf(pqWindow(col)); len(ids) != 1 || ids[0] != acVidA1 {
		t.Errorf("collection queue %v", ids)
	}

	// T04: a series with 1-3 watched starts at 4; Previous reaches 3
	seriesID := uuid.NewString()
	exec(`INSERT INTO series (id, library_id, name) VALUES ($1,$2,'Fiqh')`, seriesID, acLibA)
	var eps []string
	rows, err := e.db.Query(`SELECT id FROM videos WHERE library_id = $1 AND title LIKE 'v00%' ORDER BY title LIMIT 10`, acLibA)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		eps = append(eps, id)
	}
	rows.Close()
	for i, id := range eps {
		exec(`INSERT INTO series_episodes (series_id, video_id, season, episode) VALUES ($1,$2,1,$3)`, seriesID, id, i+1)
	}
	e.want(http.StatusOK, "member", "POST", "/api/me/watched", map[string]any{"video_ids": eps[:3], "watched": true})
	ser := e.want(http.StatusCreated, "member", "POST", "/api/me/play-queues", map[string]any{"source": map[string]any{"kind": "series", "id": seriesID}})
	if cur := pqCurrent(t, ser); cur.id != eps[3] {
		t.Errorf("series starts at %s, want episode 4", cur.id)
	}
	if w := pqWindow(ser); len(w) != 10 || w[2].id != eps[2] || w[2].pos != 2 {
		t.Errorf("series window %v", w)
	}
	// a season (series_seasons row) the same; a video plays on through its series
	seasonID := uuid.NewString()
	exec(`INSERT INTO series_seasons (id, series_id, library_id, season, kind, name, membership_hash, detector_version)
		VALUES ($1,$2,$3,1,'episodic','Season 1','x',1)`, seasonID, seriesID, acLibA)
	// the episodes route names the season's row, which a season queue takes
	epsDoc := e.want(http.StatusOK, "member", "GET", "/api/series/"+seriesID+"/episodes?sort=episode", nil)
	if ss := epsDoc["seasons"].([]any); len(ss) != 1 || ss[0].(map[string]any)["id"] != seasonID || ss[0].(map[string]any)["kind"] != "episodic" {
		t.Errorf("episodes' seasons %v", epsDoc["seasons"])
	}
	sea := e.want(http.StatusCreated, "member", "POST", "/api/me/play-queues", map[string]any{"source": map[string]any{"kind": "season", "id": seasonID}})
	if cur := pqCurrent(t, sea); cur.id != eps[3] {
		t.Errorf("season starts at %s", cur.id)
	}
	vq := e.want(http.StatusCreated, "member", "POST", "/api/me/play-queues", map[string]any{"source": map[string]any{"kind": "video", "id": eps[6]}})
	if cur := pqCurrent(t, vq); cur.id != eps[6] {
		t.Errorf("video queue starts at %s", cur.id)
	}
	if nx, _ := pqNext(vq); nx.id != eps[7] {
		t.Errorf("video queue goes on to %s, want episode 8", nx.id)
	}
	// a playlist, the caller's own
	pl := e.want(http.StatusCreated, "member", "POST", "/api/me/playlists", map[string]any{"name": "pq", "video_ids": []string{eps[1], eps[0]}})
	plq := e.want(http.StatusCreated, "member", "POST", "/api/me/play-queues", map[string]any{"source": map[string]any{"kind": "playlist", "id": pl["id"]}})
	if ids := idsOf(pqWindow(plq)); len(ids) != 2 {
		t.Errorf("playlist queue %v", ids)
	}
	e.want(http.StatusNotFound, "other", "POST", "/api/me/play-queues", map[string]any{"source": map[string]any{"kind": "playlist", "id": pl["id"]}})

	// T05: Play next twice, then Add to queue: current, n1, n2, … the rest held, added, next chunk
	sid2 := ser["id"].(string)
	e.want(http.StatusOK, "member", "POST", "/api/me/play-queues/"+sid2+"/items", map[string]any{"video_ids": []string{acVidA1}, "where": "next"})
	e.want(http.StatusOK, "member", "POST", "/api/me/play-queues/"+sid2+"/items", map[string]any{"video_ids": []string{eps[9]}, "where": "next"})
	added := e.want(http.StatusOK, "member", "POST", "/api/me/play-queues/"+sid2+"/items", map[string]any{"video_ids": []string{eps[0]}, "where": "end"})
	w := pqWindow(added)
	ci := 0
	for i, it := range w {
		if it.pos == int(added["current_pos"].(float64)) {
			ci = i
		}
	}
	if w[ci].id != eps[3] || w[ci+1].id != acVidA1 || w[ci+1].origin != "next" || w[ci+2].id != eps[9] || w[ci+3].id != eps[4] {
		t.Errorf("after Play next ×2: %v", w)
	}
	if last := w[len(w)-1]; last.id != eps[0] || last.origin != "added" {
		t.Errorf("Add to queue went to %v", last)
	}
	e.want(http.StatusNotFound, "member", "POST", "/api/me/play-queues/"+sid2+"/items", map[string]any{"video_ids": []string{acVidB1}, "where": "next"})
	e.want(http.StatusUnprocessableEntity, "member", "POST", "/api/me/play-queues/"+sid2+"/items", map[string]any{"video_ids": []string{acVidA1}, "where": "middle"})

	// T06: two devices on one queue: the second move on the same version is a 409 with the state
	first := e.want(http.StatusOK, "member", "GET", "/api/me/play-queues/"+sid2, nil)
	nx, _ := pqNext(first)
	pqMove(t, e, "member", first, nx.pos)
	code, stale := e.do("member", "PUT", "/api/me/play-queues/"+sid2+"/current", map[string]any{"pos": nx.pos, "version": first["version"]})
	if code != http.StatusConflict || stale["title"] != "stale_queue" || stale["queue"] == nil {
		t.Errorf("stale move: %d %v", code, stale)
	}

	// the latest queue is offered on another device
	cur := e.want(http.StatusOK, "member", "GET", "/api/me/play-queues/current", nil)
	if cur["id"] != sid2 {
		t.Errorf("current queue %v, want %s", cur["id"], sid2)
	}
	if code, _ := e.do("other", "GET", "/api/me/play-queues/current", nil); code != http.StatusNoContent {
		t.Errorf("a user with no queue: %d", code)
	}

	// T07: no session 401; another user and the admin 404 on every route
	base := "/api/me/play-queues/" + sid2
	routes := []struct{ method, path string }{
		{"GET", base}, {"PUT", base + "/current"}, {"POST", base + "/items"}, {"POST", base + "/reshuffle"}, {"DELETE", base},
	}
	for _, rt := range routes {
		body := map[string]any{"pos": 0, "version": 1, "video_ids": []string{acVidA1}, "where": "end"}
		for _, who := range []string{"other", "admin"} {
			if code, _ := e.do(who, rt.method, rt.path, body); code != http.StatusNotFound {
				t.Errorf("%s %s %s: %d, want 404", who, rt.method, rt.path, code)
			}
		}
	}
	for _, rt := range append(routes, struct{ method, path string }{"POST", "/api/me/play-queues"}, struct{ method, path string }{"GET", "/api/me/play-queues/current"}) {
		if code, _ := e.do("", rt.method, rt.path, map[string]any{}); code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s: %d, want 401", rt.method, rt.path, code)
		}
	}
	// a library the caller cannot read is 404, as its routes answer
	e.want(http.StatusNotFound, "member", "POST", "/api/me/play-queues", map[string]any{"source": map[string]any{"kind": "library", "id": acLibB}})
	e.want(http.StatusNotFound, "member", "POST", "/api/me/play-queues", map[string]any{"source": map[string]any{"kind": "collection", "id": acColB}})

	// T08: the member loses library A mid-queue: the next chunk writes none of it,
	// and the window shows none of what was written
	lost := e.want(http.StatusCreated, "member", "POST", "/api/me/play-queues", map[string]any{"source": map[string]any{"kind": "library", "id": acLibA}})
	lid := lost["id"].(string)
	exec(`UPDATE play_queues SET current_pos = 460 WHERE id = $1`, lid)
	moved := e.want(http.StatusOK, "member-lost-a", "PUT", "/api/me/play-queues/"+lid+"/current", map[string]any{"pos": 470, "version": 1})
	if n := count(`SELECT count(*) FROM play_queue_items WHERE queue_id = $1`, lid); n != playqueue.ChunkSize {
		t.Errorf("a chunk was written for a library the owner lost: %d items", n)
	}
	if items := moved["items"].([]any); len(items) != 0 {
		t.Errorf("the window shows %d items of a lost library", len(items))
	}

	// T09: the maintenance sweep deletes queues unused for over 7 days
	exec(`UPDATE play_queues SET used_at = now() - interval '8 days' WHERE id = $1`, lid)
	if n, err := playqueue.SweepOnce(context.Background(), e.db, time.Now()); err != nil || n != 1 {
		t.Errorf("sweep: %d, %v", n, err)
	}
	if n := count(`SELECT count(*) FROM play_queue_items WHERE queue_id = $1`, lid); n != 0 {
		t.Errorf("swept queue kept %d items", n)
	}
	e.want(http.StatusNoContent, "member", "DELETE", base, nil)
	e.want(http.StatusNotFound, "member", "GET", base, nil)
}

func idsOf(items []pqItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.id
	}
	return out
}
