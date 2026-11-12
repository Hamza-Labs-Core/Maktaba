package router_test

// Epic 57 story 57.03, the surfaces its first pass left closed: search, the live feeds, tags, a
// library's numbers, curating writes and a stream's progress, behind the production router on a
// migrated Postgres (MAKTABA_TEST_PG_URL). Each is tried as no one, a viewer, the restricted
// child (on a bearer token, a cookie, an API token, and with every library), its guardian,
// another guardian and an admin.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

var (
	pfChildren = []string{"child", "child-cookie", "child-token", "child-all"}
	pfOthers   = []string{"viewer", "guardian", "other-guardian", "admin"}
	pfHidden   = []string{pfTeen, pfTeenName, pfTeen2, "Late-Episode-Title", pfUnrated, "Unrated-Title", "teen-secret", "unrated-secret"}
)

func (e *pfEnv) names(what, body string) {
	e.t.Helper()
	for _, h := range pfHidden {
		if strings.Contains(body, h) {
			e.t.Errorf("%s names %s: %s", what, h, body)
		}
	}
}

// transcripts gives each fixture video a transcript with the word zarqa in it.
func (e *pfEnv) transcripts() {
	e.t.Helper()
	for i, v := range []struct{ id, text string }{{pfKid, "kid-safe zarqa"}, {pfTeen, "teen-secret zarqa"}, {pfUnrated, "unrated-secret zarqa"}} {
		var track int64
		if err := e.db.QueryRow(`INSERT INTO audio_tracks (video_id, track_index, codec, channels, language, is_default)
			VALUES ($1, 0, 'aac', 2, 'ara', true) RETURNING id`, v.id).Scan(&track); err != nil {
			e.t.Fatal(err)
		}
		tid := uuid.NewString()
		e.exec(`INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model, is_active) VALUES ($1,$2,$3,'ar','test','m',true)`, tid, v.id, track)
		e.exec(`INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text) VALUES ($1, 0, $2, $3, $4), ($1, 1, $3, $3 + 5, $4 || ' again')`,
			tid, i*10, i*10+9, v.text)
	}
	e.exec(`UPDATE videos SET title = title || ' zarqa' WHERE id IN ($1,$2,$3)`, pfKid, pfTeen, pfUnrated)
}

type pfSearch struct {
	SearchID        string `json:"search_id"`
	VideosTotal     int    `json:"videos_total"`
	TranscriptTotal int    `json:"transcript_total"`
	Total           int    `json:"total"`
}

func (e *pfEnv) search(who, body string) (pfSearch, string) {
	e.t.Helper()
	rec := e.raw(who, http.MethodPost, "/api/search", body)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("%s search: %d %s", who, rec.Code, rec.Body.String())
	}
	var out pfSearch
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	return out, rec.Body.String()
}

// T04: search, its pages, its passages and recent searches show a restricted child only what
// it may see, with totals to match; everyone else finds everything.
func TestP57GapSearch(t *testing.T) {
	e := setupFilter(t)
	e.transcripts()
	for _, who := range pfChildren {
		for _, mode := range []string{"fts", "hybrid", "semantic"} {
			res, body := e.search(who, `{"q":"zarqa","mode":"`+mode+`","view":"list","include":["youtube"]}`)
			e.names(who+" search "+mode, body)
			wantT := 1
			if mode == "semantic" { // no meaning search is wired here: only titles answer
				wantT = 0
			}
			if res.VideosTotal != 1 || res.TranscriptTotal != wantT || !strings.Contains(body, pfKid) {
				t.Errorf("%s %s: %d titles, %d transcripts, want 1 and %d: %s", who, mode, res.VideosTotal, res.TranscriptTotal, wantT, body)
			}
			if strings.Contains(body, `"reason":"disabled"`) == false {
				t.Errorf("%s %s: the YouTube block was not withheld: %s", who, mode, body)
			}
			for _, list := range []string{"titles", "transcripts"} {
				code, page := e.get(who, "/api/search/results/"+res.SearchID+"?list="+list)
				e.names(who+" results "+list, page)
				var p struct {
					Total int `json:"total"`
				}
				_ = json.Unmarshal([]byte(page), &p)
				if want := map[string]int{"titles": 1, "transcripts": wantT}[list]; code != http.StatusOK || p.Total != want {
					t.Errorf("%s %s results %s: %d total %d, want %d", who, mode, list, code, p.Total, want)
				}
			}
			// one hidden video's passages: as a video that is not in the search
			for _, id := range []string{pfTeen, pfUnrated} {
				e.same(who, http.MethodGet, "/api/search/passages?sid="+res.SearchID+"&video_id={id}", id, "")
			}
			if code, body := e.get(who, "/api/search/passages?sid="+res.SearchID+"&video_id="+pfKid); code != http.StatusOK {
				t.Errorf("%s: the allowed video's passages: %d %s", who, code, body)
			}
		}
		code, body := e.get(who, "/api/search/recent")
		e.names(who+" recent", body)
		if code != http.StatusOK || strings.Contains(body, `"title_count":3`) || !strings.Contains(body, `"title_count":1`) {
			t.Errorf("%s recent: %d %s", who, code, body)
		}
		if code, body := e.get(who, "/api/search/suggest?q=zar"); code != http.StatusOK {
			t.Errorf("%s suggest: %d %s", who, code, body)
		} else {
			e.names(who+" suggest", body)
		}
		if rec := e.raw(who, http.MethodDelete, "/api/search/recent", ""); rec.Code/100 != 2 {
			t.Errorf("%s clearing its recent searches: %d", who, rec.Code)
		}
	}
	for _, who := range pfOthers {
		res, body := e.search(who, `{"q":"zarqa","mode":"fts","view":"list"}`)
		if res.VideosTotal != 3 || res.TranscriptTotal != 3 || !strings.Contains(body, pfTeen) || !strings.Contains(body, "teen-secret") {
			t.Errorf("%s: %d titles, %d transcripts, want 3 and 3: %s", who, res.VideosTotal, res.TranscriptTotal, body)
		}
		if code, _ := e.get(who, "/api/search/passages?sid="+res.SearchID+"&video_id="+pfTeen); code != http.StatusOK {
			t.Errorf("%s: the 16+ video's passages: %d", who, code)
		}
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/search", `{"q":"zarqa"}`}, {http.MethodGet, "/api/search/recent", ""},
		{http.MethodGet, "/api/search/suggest?q=zar", ""}, {http.MethodGet, "/api/search/results/abcdefghijklmnopqrstuvwx?list=titles", ""},
		{http.MethodGet, "/api/search/passages?sid=abcdefghijklmnopqrstuvwx&video_id=" + pfTeen, ""},
	} {
		if rec := e.raw("", c.method, c.path, c.body); rec.Code/100 == 2 || strings.Contains(rec.Body.String(), pfTeenName) {
			t.Errorf("no one %s %s: %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}

	// a search kept under a wider limit loses what a block hides, on its next read
	e.exec(`UPDATE parental_policies SET max_tier = 18, version = version + 1 WHERE user_id = $1`, pfChild)
	e.gate.Cache().Invalidate("")
	kept, body := e.search("child", `{"q":"zarqa","mode":"fts","view":"list"}`)
	if kept.VideosTotal != 2 || kept.TranscriptTotal != 2 || !strings.Contains(body, pfTeen) || strings.Contains(body, pfUnrated) {
		t.Fatalf("under an 18+ limit: %d titles, %d transcripts: %s", kept.VideosTotal, kept.TranscriptTotal, body)
	}
	e.rule(parental.EffectBlock, parental.SubjectVideo, pfTeen)
	for _, list := range []string{"titles", "transcripts"} {
		code, page := e.get("child", "/api/search/results/"+kept.SearchID+"?list="+list)
		if code != http.StatusOK || strings.Contains(page, pfTeen) || strings.Contains(page, "teen-secret") || !strings.Contains(page, `"total":1`) {
			t.Errorf("after the block, results %s: %d %s", list, code, page)
		}
	}
	e.same("child", http.MethodGet, "/api/search/passages?sid="+kept.SearchID+"&video_id={id}", pfTeen, "")
	if _, body := e.get("child", "/api/search/recent"); strings.Contains(body, `"title_count":2`) {
		t.Errorf("after the block, recent still counts the blocked video: %s", body)
	}
	// the guardian's own search is untouched by the child's rules
	if res, _ := e.search("guardian", `{"q":"zarqa","mode":"fts"}`); res.VideosTotal != 3 {
		t.Errorf("the guardian finds %d titles, want 3", res.VideosTotal)
	}
}

// pfFeed is one open live feed.
type pfFeed struct {
	mu     sync.Mutex
	rec    *httptest.ResponseRecorder
	cancel context.CancelFunc
	done   chan struct{}
}

func (f *pfFeed) Header() http.Header { return f.rec.Header() }
func (f *pfFeed) WriteHeader(c int)   { f.mu.Lock(); f.rec.WriteHeader(c); f.mu.Unlock() }
func (f *pfFeed) Flush()              {}
func (f *pfFeed) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rec.Write(b)
}

func (f *pfFeed) body() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rec.Body.String()
}

// has waits for s on the feed.
func (f *pfFeed) has(s string) bool {
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if strings.Contains(f.body(), s) {
			return true
		}
	}
	return false
}

// over reports whether the server ended the feed.
func (f *pfFeed) over() bool {
	select {
	case <-f.done:
		return true
	case <-time.After(5 * time.Second):
		return false
	}
}

func (f *pfFeed) close() string {
	f.cancel()
	<-f.done
	return f.body()
}

// feed opens path as who and waits until it listens on channel (or was answered).
func (e *pfEnv) feed(who, path, channel string) *pfFeed {
	e.t.Helper()
	before := e.hub.SubscriberCount(channel)
	ctx, cancel := context.WithCancel(context.Background())
	f := &pfFeed{rec: httptest.NewRecorder(), cancel: cancel, done: make(chan struct{})}
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	req.Header.Set("X-Test-As", who)
	go func() {
		defer close(f.done)
		e.h.ServeHTTP(f, req)
	}()
	e.t.Cleanup(func() { f.close() })
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		select {
		case <-f.done:
			return f
		default:
		}
		if e.hub.SubscriberCount(channel) > before {
			return f
		}
	}
	e.t.Fatalf("%s %s never listened on %s", who, path, channel)
	return nil
}

func (e *pfEnv) publish(channel, typ string, payload map[string]any) {
	e.hub.Publish(channel, ws.Event{Type: typ, Payload: payload})
}

// T07: the live feeds send a restricted child only the events about what it may see, with the
// limit as it is when the event comes; a feed whose library is blocked ends; an unreadable
// policy ends it too.
func TestP57GapLiveFeeds(t *testing.T) {
	e := setupFilter(t)
	const home = ws.HomeChannel
	libChan := "library:" + pfLibA
	end := 0
	// mark publishes an event every account is sent and waits for it on each feed: everything
	// published before it has been decided by then
	mark := func(channel string, feeds ...*pfFeed) {
		t.Helper()
		end++
		tag := "mark-" + strings.Repeat("x", end)
		e.publish(channel, ws.EventCapabilitiesChanged, map[string]any{"etag": tag + "."})
		for _, f := range feeds {
			if !f.has(tag + ".") {
				t.Fatalf("a feed on %s never got %s: %s", channel, tag, f.body())
			}
		}
	}

	for _, who := range pfChildren {
		hf, lf := e.feed(who, "/ws/home", home), e.feed(who, "/ws/library/"+pfLibA, libChan)
		vf := e.feed("viewer", "/ws/library/"+pfLibA, libChan)
		for _, ch := range []string{home, libChan} {
			e.publish(ch, "video.added", map[string]any{"video_id": pfTeen, "title": pfTeenName})
			e.publish(ch, "video.added", map[string]any{"video": map[string]any{"id": pfUnrated, "title": "Unrated-Title"}})
			e.publish(ch, "videos.changed", map[string]any{"video_ids": []any{pfKid, pfTeen}})
			e.publish(ch, "series.changed", map[string]any{"series_id": pfSeries16, "name": "Pf late show"})
			e.publish(ch, "collection.changed", map[string]any{"collection_id": pfCol16})
			e.publish(ch, "row.changed", map[string]any{"items": []any{map[string]any{"video_id": pfKid}, map[string]any{"video_id": pfTeen2}}})
			e.publish(ch, "something.new", map[string]any{"note": "names-nothing " + pfTeenName})
			e.publish(ch, "video.broken", map[string]any{"video_id": "not-an-id", "title": pfTeenName})
			e.publish(ch, "video.added", map[string]any{"video_id": pfKid, "title": "Kid-Title"})
			e.publish(ch, "series.changed", map[string]any{"series_id": pfSeries, "name": "Pf show"})
		}
		mark(home, hf)
		mark(libChan, lf, vf)
		for name, f := range map[string]*pfFeed{"/ws/home": hf, "/ws/library": lf} {
			body := f.body()
			e.names(who+" "+name, body)
			for _, s := range []string{pfSeries16, "Pf late show", pfCol16, "names-nothing", "not-an-id"} {
				if strings.Contains(body, s) {
					t.Errorf("%s %s was sent %s: %s", who, name, s, body)
				}
			}
			if !strings.Contains(body, "Kid-Title") || !strings.Contains(body, "Pf show") {
				t.Errorf("%s %s lacks the events about what it may see: %s", who, name, body)
			}
		}
		if body := vf.body(); !strings.Contains(body, pfTeenName) || !strings.Contains(body, "names-nothing") || !strings.Contains(body, "Pf late show") {
			t.Errorf("the viewer's feed lacks events: %s", body)
		}

		// the policy changes while the feeds are open: an allow, then a block on the library
		if who == "child" {
			e.rule(parental.EffectAllow, parental.SubjectVideo, pfTeen)
			e.publish(libChan, "video.added", map[string]any{"video_id": pfTeen, "title": "now-allowed"})
			mark(libChan, lf)
			if !strings.Contains(lf.body(), "now-allowed") {
				t.Errorf("after an allow the feed was not sent the video's event: %s", lf.body())
			}
			e.clearRules()
			e.publish(libChan, "video.added", map[string]any{"video_id": pfTeen, "title": "hidden-again"})
			mark(libChan, lf)
			if strings.Contains(lf.body(), "hidden-again") {
				t.Errorf("after the allow was removed the feed was still sent the video's event")
			}
			e.rule(parental.EffectBlock, parental.SubjectLibrary, pfLibA)
			e.publish(libChan, "video.added", map[string]any{"video_id": pfKid, "title": "after-library-block"})
			if !lf.over() || strings.Contains(lf.body(), "after-library-block") {
				t.Errorf("a feed of a library blocked while it was open did not end, or sent on: %s", lf.body())
			}
			// and it does not open again: the answer is a missing library's
			e.same(who, http.MethodGet, "/ws/library/{id}", pfLibA, "")
			e.clearRules()
		}
		hf.close()
		lf.close()
		vf.close()
	}

	// everyone else hears everything; no one hears nothing
	for _, who := range pfOthers {
		f := e.feed(who, "/ws/home", home)
		e.publish(home, "video.added", map[string]any{"video_id": pfTeen, "title": pfTeenName})
		e.publish(home, "something.new", map[string]any{"note": "names-nothing"})
		mark(home, f)
		if body := f.close(); !strings.Contains(body, pfTeenName) || !strings.Contains(body, "names-nothing") {
			t.Errorf("%s /ws/home: %s", who, body)
		}
	}
	for _, p := range []string{"/ws/home", "/ws/library/" + pfLibA} {
		if rec := e.raw("", http.MethodGet, p, ""); rec.Code/100 == 2 {
			t.Errorf("no one GET %s: %d", p, rec.Code)
		}
	}

	// the policy cannot be read: the feed ends at the next event, restricted or not
	for _, who := range []string{"child", "viewer"} {
		f := e.feed(who, "/ws/home", home)
		cache := e.gate.Cache()
		dead, err := sql.Open("postgres", "postgres://nobody:x@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
		if err != nil {
			t.Fatal(err)
		}
		e.gate.SetCache(parental.NewCache(parental.NewStore(dead)))
		e.publish(home, "video.added", map[string]any{"video_id": pfKid, "title": "while-unreadable"})
		ended := f.over()
		e.gate.SetCache(cache)
		_ = dead.Close()
		if !ended || strings.Contains(f.body(), "while-unreadable") {
			t.Errorf("%s: with the policy unreadable the feed did not end, or sent on: %s", who, f.body())
		}
	}
}

// Tags and a library's numbers are counted over what the child may see.
func TestP57GapTagsAndStats(t *testing.T) {
	e := setupFilter(t)
	e.exec(`INSERT INTO tags (id, name, name_norm) VALUES
		('00000000-0000-4000-8000-0000573e0001','kid-tag','kid-tag'),
		('00000000-0000-4000-8000-0000573e0002','teen-secret-tag','teen-secret-tag'),
		('00000000-0000-4000-8000-0000573e0003','shared-tag','shared-tag')`)
	e.exec(`INSERT INTO video_tags (video_id, tag_id) VALUES
		($1,'00000000-0000-4000-8000-0000573e0001'), ($2,'00000000-0000-4000-8000-0000573e0002'),
		($1,'00000000-0000-4000-8000-0000573e0003'), ($2,'00000000-0000-4000-8000-0000573e0003')`, pfKid, pfTeen)
	stats := func(who string) (int, map[string]any) {
		t.Helper()
		code, body := e.get(who, "/api/libraries/"+pfLibA+"/stats")
		var out map[string]any
		_ = json.Unmarshal([]byte(body), &out)
		return code, out
	}
	// the viewer first: the shared stats row is written with every video in it
	for _, who := range pfOthers {
		if code, out := stats(who); code != http.StatusOK || out["total_videos"] != float64(4) {
			t.Fatalf("%s stats: %d %v, want 4 videos", who, code, out)
		}
		if code, body := e.get(who, "/api/tags"); code != http.StatusOK || !strings.Contains(body, "teen-secret-tag") || !strings.Contains(body, "kid-tag") {
			t.Errorf("%s tags: %d %s", who, code, body)
		}
	}
	for _, who := range pfChildren {
		code, body := e.get(who, "/api/tags")
		if code != http.StatusOK || strings.Contains(body, "teen-secret-tag") || !strings.Contains(body, "kid-tag") || !strings.Contains(body, "shared-tag") {
			t.Errorf("%s tags: %d %s", who, code, body)
		}
		code, out := stats(who)
		if code != http.StatusOK || out["total_videos"] != float64(1) || out["total_duration_sec"] != float64(100) || out["source_size_bytes"] != float64(1000) {
			t.Errorf("%s stats: %d %v, want the 1 video shown (100 s, 1000 bytes)", who, code, out)
		}
		if st, _ := out["by_state"].(map[string]any); len(st) != 1 || st["ready"] != float64(1) {
			t.Errorf("%s stats by state: %v", who, out["by_state"])
		}
		if lang, _ := out["by_language"].(map[string]any); len(lang) != 1 {
			t.Errorf("%s stats by language: %v", who, out["by_language"])
		}
		if out["last_sweep"] != nil || len(out["jobs"].(map[string]any)) != 0 {
			t.Errorf("%s stats carry the home's sweep or jobs: %v", who, out)
		}
	}
	// the child's read left the shared row as it was
	if code, out := stats("viewer"); code != http.StatusOK || out["total_videos"] != float64(4) {
		t.Errorf("the viewer's stats after the child's: %d %v", code, out)
	}
	// an allow counts, a block on the library hides its numbers as a missing library's
	e.rule(parental.EffectAllow, parental.SubjectVideo, pfTeen)
	if _, out := stats("child"); out["total_videos"] != float64(2) {
		t.Errorf("with an allow the child's stats count %v videos, want 2", out["total_videos"])
	}
	if _, body := e.get("child", "/api/tags"); !strings.Contains(body, "teen-secret-tag") {
		t.Errorf("with an allow the child's tags lack the video's tag: %s", body)
	}
	e.clearRules()
	e.rule(parental.EffectBlock, parental.SubjectLibrary, pfLibA)
	for _, who := range []string{"child", "child-all"} {
		e.same(who, http.MethodGet, "/api/libraries/{id}/stats", pfLibA, "")
		if _, body := e.get(who, "/api/tags"); strings.Contains(body, "kid-tag") {
			t.Errorf("%s: with the library blocked its tags still show: %s", who, body)
		}
	}
	for _, p := range []string{"/api/tags", "/api/libraries/" + pfLibA + "/stats"} {
		if code, _ := e.get("", p); code/100 == 2 {
			t.Errorf("no one GET %s: %d", p, code)
		}
	}
	// tags stay an admin's to make and remove
	for _, who := range pfChildren {
		rec := e.raw(who, http.MethodPost, "/api/tags", `{"name":"x"}`)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"restricted"`) {
			t.Errorf("%s POST /api/tags: %d %s", who, rec.Code, rec.Body.String())
		}
	}
}

// Curating collections and series is an admin's. A restricted child is refused as any other
// account is, and a hidden collection or series answers as a missing one before that.
func TestP57GapCurating(t *testing.T) {
	e := setupFilter(t)
	writes := []struct{ method, path, body, kind string }{
		{http.MethodPatch, "/api/collections/{id}", `{"name":"x"}`, "collection"},
		{http.MethodDelete, "/api/collections/{id}", ``, "collection"},
		{http.MethodPost, "/api/collections/{id}/items", `{"video_id":"` + pfKid + `"}`, "collection"},
		{http.MethodDelete, "/api/collections/{id}/items/" + pfKid, ``, "collection"},
		{http.MethodPost, "/api/collections/{id}/convert?freeze=true", ``, "collection"},
		{http.MethodPost, "/api/collections/{id}/duplicate", ``, "collection"},
		{http.MethodPut, "/api/collections/{id}/age-tier", `{"age_tier":0}`, "collection"},
		{http.MethodPatch, "/api/series/{id}", `{"name":"x"}`, "series"},
		{http.MethodPost, "/api/series/{id}/enrichment/accept-all", `{}`, "series"},
		{http.MethodPut, "/api/series/{id}/age-tier", `{"age_tier":0}`, "series"},
		{http.MethodPut, "/api/series/{id}/style", `{}`, "series"},
	}
	before := func() string {
		var s string
		if err := e.db.QueryRow(`SELECT (SELECT string_agg(name || coalesce(age_tier::text, '-'), ',' ORDER BY id) FROM collections) || '|' ||
			(SELECT string_agg(name || coalesce(age_tier::text, '-'), ',' ORDER BY id) FROM series) || '|' ||
			(SELECT count(*) FROM collection_items)`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	state := before()
	for _, who := range pfChildren {
		for _, w := range writes {
			hidden, shown := pfCol16, pfCol
			if w.kind == "series" {
				hidden, shown = pfSeries16, pfSeries
			}
			// hidden: a missing one's answer, byte for byte
			e.same(who, w.method, w.path, hidden, w.body)
			// shown: the handler's own refusal of an account that is no admin
			rec := e.raw(who, w.method, strings.ReplaceAll(w.path, "{id}", shown), w.body)
			viewer := e.raw("viewer", w.method, strings.ReplaceAll(w.path, "{id}", shown), w.body)
			if rec.Code/100 == 2 || rec.Code != viewer.Code {
				t.Errorf("%s %s %s on a collection or series it may see: %d, the viewer gets %d", who, w.method, w.path, rec.Code, viewer.Code)
			}
		}
		rec := e.raw(who, http.MethodPost, "/api/collections", `{"name":"mine","library_id":"`+pfLibA+`"}`)
		viewer := e.raw("viewer", http.MethodPost, "/api/collections", `{"name":"mine","library_id":"`+pfLibA+`"}`)
		if rec.Code != http.StatusForbidden || rec.Code != viewer.Code {
			t.Errorf("%s POST /api/collections: %d, the viewer gets %d", who, rec.Code, viewer.Code)
		}
		// the gaps of a series: counted over the episodes it may see, numbers only
		code, body := e.get(who, "/api/series/"+pfSeries+"/missing")
		if code != http.StatusOK {
			t.Errorf("%s GET series missing: %d %s", who, code, body)
		}
		e.names(who+" series missing", body)
		e.same(who, http.MethodGet, "/api/series/{id}/missing", pfSeries16, "")
	}
	for _, who := range []string{"guardian", "other-guardian", "viewer"} {
		for _, w := range writes {
			id := pfCol16
			if w.kind == "series" {
				id = pfSeries16
			}
			if rec := e.raw(who, w.method, strings.ReplaceAll(w.path, "{id}", id), w.body); rec.Code/100 == 2 {
				t.Errorf("%s %s %s: %d, want a refusal (admin only)", who, w.method, w.path, rec.Code)
			}
		}
	}
	for _, w := range writes {
		if rec := e.raw("", w.method, strings.ReplaceAll(w.path, "{id}", pfCol), w.body); rec.Code/100 == 2 {
			t.Errorf("no one %s %s: %d", w.method, w.path, rec.Code)
		}
	}
	if after := before(); after != state {
		t.Fatalf("a refused write changed something:\n  %s\n  %s", state, after)
	}
	// the admin curates: the hidden collection is there for it
	if rec := e.raw("admin", http.MethodPatch, "/api/collections/"+pfCol16, `{"name":"Pf late picks 2"}`); rec.Code/100 != 2 {
		t.Errorf("admin PATCH collection: %d %s", rec.Code, rec.Body.String())
	}
	if code, body := e.get("admin", "/api/series/"+pfSeries16+"/missing"); code != http.StatusOK {
		t.Errorf("admin GET series missing: %d %s", code, body)
	}
}

// T08: a video blocked while it plays takes no more progress, so its stream's lease is not
// renewed: the answer is a missing session's.
func TestP57GapProgressAfterBlock(t *testing.T) {
	e := setupFilter(t)
	e.exec(`UPDATE parental_policies SET max_tier = 18, version = version + 1 WHERE user_id = $1`, pfChild)
	e.gate.Cache().Invalidate("")
	rec := e.raw("child", http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfTeen+`"}`)
	var sess struct {
		ID string `json:"session_id"`
	}
	if rec.Code/100 != 2 || json.Unmarshal(rec.Body.Bytes(), &sess) != nil || sess.ID == "" {
		t.Fatalf("open: %d %s", rec.Code, rec.Body.String())
	}
	e.exec(`INSERT INTO streaming_sessions (id, user_id, video_id, mode, opened_at, last_seen_at) VALUES ($1,$2,$3,'direct',now(),now())
		ON CONFLICT (id) DO NOTHING`, sess.ID, pfChild, pfTeen)
	progress := func(who, id string) *httptest.ResponseRecorder {
		return e.raw(who, http.MethodPost, "/api/stream/sessions/"+id+"/progress", `{"position_sec":5}`)
	}
	if rec := progress("child", sess.ID); rec.Code/100 != 2 {
		t.Fatalf("progress before the block: %d %s", rec.Code, rec.Body.String())
	}
	e.rule(parental.EffectBlock, parental.SubjectVideo, pfTeen)
	missing := uuid.NewString()
	got, want := progress("child", sess.ID), progress("child", missing)
	norm := func(rec *httptest.ResponseRecorder, id string) string {
		return pfRequestID.ReplaceAllString(strings.ReplaceAll(rec.Body.String(), id, "ID"), "R")
	}
	if got.Code != http.StatusNotFound || got.Code != want.Code || norm(got, sess.ID) != norm(want, missing) {
		t.Errorf("progress after the block: %d %s; on a missing session: %d %s", got.Code, got.Body.String(), want.Code, want.Body.String())
	}
	// the admin's own post on the session is the admin's to make
	if rec := progress("admin", sess.ID); rec.Code == http.StatusNotFound {
		t.Errorf("the admin's progress on the session: %d %s", rec.Code, rec.Body.String())
	}
}
