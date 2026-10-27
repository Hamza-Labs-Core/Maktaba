package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/rules"
)

const (
	userA = "00000000-0000-4000-8000-00000000000a"
	userB = "00000000-0000-4000-8000-00000000000b"
	libA  = "10000000-0000-4000-8000-00000000000a"
	libB  = "10000000-0000-4000-8000-00000000000b"
)

// migrationUp returns the Up statements of a shipped migration file.
func migrationUp(t *testing.T, name string) []string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "shared", "db", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	up := string(raw)
	if i := strings.Index(up, "-- +goose Down"); i >= 0 {
		up = up[:i]
	}
	var out []string
	for _, part := range strings.Split(up, "-- +goose StatementBegin") {
		if i := strings.Index(part, "-- +goose StatementEnd"); i >= 0 {
			out = append(out, part[:i])
		}
	}
	return out
}

// searchDB is a SQLite database with the tables search reads (their columns)
// and the shipped search_runs twin (slot 0148).
func searchDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, st := range []string{
		`CREATE TABLE libraries (id TEXT PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT NOT NULL, title TEXT,
			filename TEXT NOT NULL DEFAULT '', duration_sec REAL, detected_language TEXT, deleted_at TEXT)`,
		`CREATE TABLE transcripts (id TEXT PRIMARY KEY, video_id TEXT NOT NULL, is_active INTEGER NOT NULL DEFAULT 1)`,
		`CREATE TABLE transcript_segments (id INTEGER PRIMARY KEY AUTOINCREMENT, transcript_id TEXT NOT NULL,
			seq INTEGER NOT NULL, start_sec REAL NOT NULL, end_sec REAL NOT NULL, text TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(st); err != nil {
			t.Fatal(err)
		}
	}
	for _, st := range migrationUp(t, "0148_search_runs.sqlite.sql") {
		if _, err := db.Exec(st); err != nil {
			t.Fatalf("0148 twin: %v\n%s", err, st)
		}
	}
	return db
}

// fixture writes libraries, videos and transcripts.
type fixture struct {
	t   *testing.T
	db  *sql.DB
	seq int
}

func (f *fixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(q, args...); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
}

func (f *fixture) library(id, name string) {
	f.exec(`INSERT INTO libraries (id, name) VALUES ($1, $2)`, id, name)
}

func (f *fixture) video(id, lib, title string, dur float64) {
	f.exec(`INSERT INTO videos (id, library_id, title, filename, duration_sec) VALUES ($1, $2, $3, $4, $5)`,
		id, lib, title, id+".mp4", dur)
}

// transcript adds a transcript of video with one segment per text, 10 s apart.
func (f *fixture) transcript(id, video string, active bool, texts ...string) {
	f.exec(`INSERT INTO transcripts (id, video_id, is_active) VALUES ($1, $2, $3)`, id, video, active)
	if len(texts) == 0 {
		return
	}
	// one statement for every segment: a row per round trip put the paging
	// tests past the unit tier's per-test budget under -race
	var q strings.Builder
	q.WriteString(`INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text) VALUES `)
	args := make([]any, 0, 5*len(texts))
	for i, text := range texts {
		f.seq++
		if i > 0 {
			q.WriteString(", ")
		}
		n := len(args)
		fmt.Fprintf(&q, "($%d, $%d, $%d, $%d, $%d)", n+1, n+2, n+3, n+4, n+5)
		args = append(args, id, i, float64(i*10), float64(i*10+9), text)
	}
	f.exec(q.String(), args...)
}

func repeat(text string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = text
	}
	return out
}

// router mounts the handler like the API does.
func router(h *Handler) http.Handler {
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

func as(req *http.Request, p *principal.Principal) *http.Request {
	return req.WithContext(principal.WithPrincipal(req.Context(), p))
}

var (
	admin  = &principal.Principal{UserID: userA, IsAdmin: true, AccessAllLibraries: true}
	adminB = &principal.Principal{UserID: userB, IsAdmin: true, AccessAllLibraries: true}
)

func call(t *testing.T, h http.Handler, p *principal.Principal, method, url, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, url, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, url, nil)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, as(req, p))
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder, want int) T {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode: %v: %s", err, rec.Body.String())
	}
	return v
}

type resultsPage struct {
	SearchID string `json:"search_id"`
	Total    int    `json:"total"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
	Items    []struct {
		VideoID     string    `json:"video_id"`
		LibraryName string    `json:"library_name"`
		Matches     int       `json:"matches"`
		InTitle     bool      `json:"in_title"`
		Passages    []Passage `json:"passages"`
	} `json:"items"`
}

func videoIDs[T any](items []T, id func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = id(it)
	}
	return out
}

// The transcript list has one entry per video with the TRUE number of its
// matching segments (more than the old 50-segment cap), only the active
// transcript and live videos count, and each video carries its best passages.
func TestSearch_GroupsPerVideoWithFullCounts(t *testing.T) {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.library(libA, "Lessons")
	f.video("va", libA, "Fiqh lesson 1", 3600)
	f.video("vb", libA, "Seerah", 1800)
	f.video("vgone", libA, "Gone", 60)
	f.exec(`UPDATE videos SET deleted_at = '2026-01-01' WHERE id = 'vgone'`)
	f.transcript("ta", "va", true, repeat("about prayer here", 120)...)
	f.transcript("ta-old", "va", false, repeat("prayer in the old transcript", 40)...)
	f.transcript("tb", "vb", true, "prayer", "nothing", "prayer again", "a prayer")
	f.transcript("tgone", "vgone", true, repeat("prayer", 10)...)

	h := router(&Handler{DB: db})
	resp := decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search", `{"q":"prayer","mode":"fts","view":"list"}`), 200)
	if resp.SearchID == "" {
		t.Fatal("no search_id")
	}
	if resp.TranscriptTotal != 2 || len(resp.TranscriptVideos) != 2 {
		t.Fatalf("want 2 videos, got total %d %+v", resp.TranscriptTotal, resp.TranscriptVideos)
	}
	got := map[string]TranscriptVideo{}
	for _, v := range resp.TranscriptVideos {
		if _, dup := got[v.VideoID]; dup {
			t.Fatalf("video %s twice", v.VideoID)
		}
		got[v.VideoID] = v
	}
	if got["va"].Matches != 120 || got["vb"].Matches != 3 {
		t.Fatalf("matches: va %d (want 120), vb %d (want 3)", got["va"].Matches, got["vb"].Matches)
	}
	if n := len(got["va"].Passages); n != 3 {
		t.Fatalf("want the top 3 passages, got %d", n)
	}
	p := got["vb"].Passages[0]
	if !strings.Contains(p.Snippet, "<mark>prayer</mark>") || p.EndSec <= p.StartSec {
		t.Fatalf("passage %+v", p)
	}
	if got["va"].LibraryName != "Lessons" || got["va"].PosterURL != "/api/videos/va/poster" ||
		got["va"].DurationSec == nil || *got["va"].DurationSec != 3600 {
		t.Fatalf("card %+v", got["va"].VideoCard)
	}
	// deprecated flat hits: still there, best first
	if len(resp.Hits) == 0 || resp.Total != len(resp.Hits) {
		t.Fatalf("hits %+v", resp.Hits)
	}
}

// Videos rank by their best passage first, then by how many passages match.
func TestSearch_OrdersByScoreThenCount(t *testing.T) {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.library(libA, "L")
	f.video("v-best", libA, "one", 60)
	f.video("v-many", libA, "two", 60)
	f.video("v-few", libA, "three", 60)
	// SQLite ranks a passage by how often the query occurs in it
	f.transcript("t1", "v-best", true, "zakat zakat zakat")
	f.transcript("t2", "v-many", true, repeat("zakat", 5)...)
	f.transcript("t3", "v-few", true, "zakat", "zakat")
	h := router(&Handler{DB: db})
	resp := decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search", `{"q":"zakat","mode":"fts"}`), 200)
	ids := videoIDs(resp.TranscriptVideos, func(v TranscriptVideo) string { return v.VideoID })
	if !reflect.DeepEqual(ids, []string{"v-best", "v-many", "v-few"}) {
		t.Fatalf("order %v", ids)
	}
	if resp.TranscriptVideos[0].BestScore <= resp.TranscriptVideos[1].BestScore {
		t.Fatalf("scores %+v", resp.TranscriptVideos)
	}
}

func TestSortVideos_ScoreThenCountThenID(t *testing.T) {
	vs := []savedVideo{{V: "c", S: 1, N: 2}, {V: "b", S: 1, N: 5}, {V: "a", S: 2, N: 1}, {V: "d", S: 1, N: 5}}
	sortVideos(vs)
	if got := videoIDs(vs, func(v savedVideo) string { return v.V }); !reflect.DeepEqual(got, []string{"a", "b", "d", "c"}) {
		t.Fatalf("order %v", got)
	}
}

// Both lists flag a video that is in the other list too.
func TestSearch_AlsoInTitleBadges(t *testing.T) {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.library(libA, "L")
	f.video("v-both", libA, "Ramadan night", 60)
	f.video("v-title", libA, "Ramadan day", 60)
	f.video("v-text", libA, "Untitled", 60)
	f.transcript("t1", "v-both", true, "in ramadan we fast")
	f.transcript("t2", "v-text", true, "ramadan again")
	h := router(&Handler{DB: db})
	resp := decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search", `{"q":"ramadan","mode":"fts"}`), 200)
	inTitle := map[string]bool{}
	for _, v := range resp.TranscriptVideos {
		inTitle[v.VideoID] = v.InTitle
	}
	if !inTitle["v-both"] || inTitle["v-text"] {
		t.Fatalf("in_title %v", inTitle)
	}
	inText := map[string]bool{}
	for _, v := range resp.Videos {
		inText[v.VideoID] = v.InTranscripts
		if !strings.Contains(v.TitleSnippet, "<mark>Ramadan</mark>") {
			t.Fatalf("title snippet %q", v.TitleSnippet)
		}
	}
	if !inText["v-both"] || inText["v-title"] || len(resp.Videos) != 2 {
		t.Fatalf("in_transcripts %v", inText)
	}
}

// A user limited to some libraries never sees another library's video, in
// either list, and a page read after the access was taken away leaves it out.
func TestSearch_RespectsLibraryACL(t *testing.T) {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.library(libA, "A")
	f.library(libB, "B")
	f.video("va", libA, "hajj a", 60)
	f.video("vb", libB, "hajj b", 60)
	f.transcript("ta", "va", true, "hajj")
	f.transcript("tb", "vb", true, "hajj")
	h := router(&Handler{DB: db})
	limited := &principal.Principal{UserID: userA, Libraries: []string{libA}}
	resp := decode[Response](t, call(t, h, limited, http.MethodPost, "/api/search", `{"q":"hajj","mode":"fts"}`), 200)
	if len(resp.Videos) != 1 || resp.Videos[0].VideoID != "va" || len(resp.TranscriptVideos) != 1 ||
		resp.TranscriptVideos[0].VideoID != "va" {
		t.Fatalf("ACL leak: %+v %+v", resp.Videos, resp.TranscriptVideos)
	}
	// a requested library the user may not see is dropped
	resp = decode[Response](t, call(t, h, limited, http.MethodPost, "/api/search",
		`{"q":"hajj","mode":"fts","filters":{"library_id":["`+libB+`"]}}`), 200)
	if len(resp.Videos)+len(resp.TranscriptVideos) != 0 || resp.SearchID != "" {
		t.Fatalf("library B leaked: %+v", resp)
	}
	// both libraries at search time, then B taken away: the page leaves vb out
	both := &principal.Principal{UserID: userA, Libraries: []string{libA, libB}}
	resp = decode[Response](t, call(t, h, both, http.MethodPost, "/api/search", `{"q":"hajj","mode":"fts"}`), 200)
	if resp.TranscriptTotal != 2 {
		t.Fatalf("want 2, got %d", resp.TranscriptTotal)
	}
	page := decode[resultsPage](t, call(t, h, limited, http.MethodGet,
		"/api/search/results/"+resp.SearchID+"?list=transcripts", ""), 200)
	if len(page.Items) != 1 || page.Items[0].VideoID != "va" {
		t.Fatalf("revoked library still shown: %+v", page.Items)
	}
	rec := call(t, h, limited, http.MethodGet, "/api/search/passages?sid="+resp.SearchID+"&video_id=vb", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("passages of a revoked video: %d", rec.Code)
	}
}

// Pages over the saved set: the page size follows the view's rule, offsets
// always name the same videos (even after the data changes), totals are the
// whole list's.
func TestResults_StablePaging(t *testing.T) {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.library(libA, "L")
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("v%02d", i)
		f.video(id, libA, "Tafsir "+id, 60)
		f.transcript("t"+id, id, true, repeat("tafsir", i+1)...)
	}
	h := router(&Handler{DB: db})
	resp := decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search", `{"q":"tafsir","mode":"fts","view":"grid"}`), 200)
	if resp.PageSize != 24 || len(resp.TranscriptVideos) != 24 || resp.TranscriptTotal != 30 ||
		resp.VideosTotal != 30 || len(resp.Videos) != 24 || resp.PassagesPageSize != 20 {
		t.Fatalf("page 1: size %d, %d/%d videos, %d/%d titles", resp.PageSize, len(resp.TranscriptVideos),
			resp.TranscriptTotal, len(resp.Videos), resp.VideosTotal)
	}
	first := videoIDs(resp.TranscriptVideos, func(v TranscriptVideo) string { return v.VideoID })
	if first[0] != "v29" { // 30 matches: the most
		t.Fatalf("order %v", first)
	}
	// more matches for the last video: the saved order must not move
	f.transcript("tnew", "v00", true, repeat("tafsir", 100)...)
	f.exec(`UPDATE transcripts SET is_active = 0 WHERE id = 'tv00'`)
	var all []string
	for off := 0; off < 30; off += 20 {
		page := decode[resultsPage](t, call(t, h, admin, http.MethodGet,
			fmt.Sprintf("/api/search/results/%s?list=transcripts&offset=%d&limit=20", resp.SearchID, off), ""), 200)
		if page.Total != 30 || page.Offset != off || page.Limit != 20 {
			t.Fatalf("page %d: %+v", off, page)
		}
		for _, it := range page.Items {
			all = append(all, it.VideoID)
		}
	}
	if len(all) != 30 || !reflect.DeepEqual(all[:24], first) || all[29] != "v00" {
		t.Fatalf("order moved: %v", all)
	}
	seen := map[string]bool{}
	for _, id := range all {
		if seen[id] {
			t.Fatalf("%s on two pages", id)
		}
		seen[id] = true
	}
	titles := decode[resultsPage](t, call(t, h, admin, http.MethodGet,
		"/api/search/results/"+resp.SearchID+"?list=titles&offset=24&limit=24", ""), 200)
	if titles.Total != 30 || len(titles.Items) != 6 {
		t.Fatalf("titles page 2: %+v", titles)
	}
	if rec := call(t, h, admin, http.MethodGet, "/api/search/results/"+resp.SearchID+"?list=bogus", ""); rec.Code != 422 {
		t.Fatalf("bad list: %d", rec.Code)
	}
}

type passagesPage struct {
	Total        int       `json:"total"`
	Offset       int       `json:"offset"`
	Limit        int       `json:"limit"`
	InTitle      bool      `json:"in_title"`
	TitleSnippet string    `json:"title_snippet"`
	Items        []Passage `json:"items"`
	Times        []float64 `json:"times"`
	Video        VideoCard `json:"video"`
}

// The passages of one video: all of them, best first or in time order, paged,
// with every match's time for the player's marks.
func TestPassages_AllOfOneVideo(t *testing.T) {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.library(libA, "L")
	f.video("va", libA, "Sabr", 60)
	f.video("vt", libA, "Sabr in the title only", 60)
	f.video("vx", libA, "Unrelated", 60)
	texts := repeat("sabr", 70)
	texts[40] = "sabr sabr sabr"
	texts = append(texts, "nothing here")
	f.transcript("ta", "va", true, texts...)
	h := router(&Handler{DB: db})
	resp := decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search", `{"q":"sabr","mode":"fts"}`), 200)
	sid := resp.SearchID

	best := decode[passagesPage](t, call(t, h, admin, http.MethodGet, "/api/search/passages?sid="+sid+"&video_id=va&limit=10", ""), 200)
	if best.Total != 70 || len(best.Items) != 10 || best.Items[0].StartSec != 400 || best.Video.Title != "Sabr" {
		t.Fatalf("by score: total %d, %d items, first at %v", best.Total, len(best.Items), best.Items[0].StartSec)
	}
	var times []float64
	for off := 0; off < 70; off += 30 {
		page := decode[passagesPage](t, call(t, h, admin, http.MethodGet,
			fmt.Sprintf("/api/search/passages?sid=%s&video_id=va&order=time&offset=%d&limit=30&include_times=1", sid, off), ""), 200)
		if len(page.Times) != 70 {
			t.Fatalf("times: %d", len(page.Times))
		}
		for _, p := range page.Items {
			times = append(times, p.StartSec)
		}
	}
	if len(times) != 70 || times[0] != 0 || times[69] != 690 {
		t.Fatalf("time order: %v", times)
	}
	for i := 1; i < len(times); i++ {
		if times[i] <= times[i-1] {
			t.Fatalf("not in time order at %d: %v", i, times)
		}
	}
	title := decode[passagesPage](t, call(t, h, admin, http.MethodGet, "/api/search/passages?sid="+sid+"&video_id=vt&order=time", ""), 200)
	if title.Total != 0 || len(title.Items) != 0 || !title.InTitle || !strings.Contains(title.TitleSnippet, "<mark>Sabr</mark>") {
		t.Fatalf("title only: %+v", title)
	}
	if rec := call(t, h, admin, http.MethodGet, "/api/search/passages?sid="+sid+"&video_id=vx", ""); rec.Code != 404 {
		t.Fatalf("video not in the search: %d", rec.Code)
	}
	if rec := call(t, h, admin, http.MethodGet, "/api/search/passages?sid="+sid+"&video_id=va&order=bogus", ""); rec.Code != 422 {
		t.Fatalf("bad order: %d", rec.Code)
	}
}

// clock is a settable NowFunc.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// Every read keeps the results another 15 minutes; 15 idle minutes expire
// them (410 search-expired), another user's id reads as expired, and the
// search runs again under a new id.
func TestSearchID_SlidingExpiryAndOwnership(t *testing.T) {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.library(libA, "L")
	f.video("va", libA, "Dua", 60)
	f.transcript("ta", "va", true, "dua")
	c := &clock{t: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	h := router(&Handler{DB: db, NowFunc: c.now})
	resp := decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search", `{"q":"dua","mode":"fts"}`), 200)
	sid := resp.SearchID
	if !resp.ExpiresAt.Equal(c.t.Add(15 * time.Minute)) {
		t.Fatalf("expires %v", resp.ExpiresAt)
	}
	page := "/api/search/results/" + sid + "?list=transcripts"
	for i := 0; i < 4; i++ { // 4 reads, 10 minutes apart: 40 minutes in all
		c.t = c.t.Add(10 * time.Minute)
		decode[resultsPage](t, call(t, h, admin, http.MethodGet, page, ""), 200)
	}
	// another user: as if it did not exist
	rec := call(t, h, adminB, http.MethodGet, page, "")
	if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), ProblemSearchExpired) {
		t.Fatalf("another user read it: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, h, adminB, http.MethodGet, "/api/search/passages?sid="+sid+"&video_id=va", ""); rec.Code != http.StatusGone {
		t.Fatalf("another user's passages: %d", rec.Code)
	}
	c.t = c.t.Add(15*time.Minute + time.Second)
	rec = call(t, h, admin, http.MethodGet, page, "")
	if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), ProblemSearchExpired) {
		t.Fatalf("idle 15 minutes: %d %s", rec.Code, rec.Body.String())
	}
	var kept sql.NullString
	if err := db.QueryRow(`SELECT result FROM search_runs WHERE id = $1`, sid).Scan(&kept); err != nil || kept.Valid {
		t.Fatalf("expired result still kept: %v %v", kept, err)
	}
	if rec := call(t, h, admin, http.MethodGet, "/api/search/results/not-an-id!?list=titles", ""); rec.Code != http.StatusGone {
		t.Fatalf("unknown id: %d", rec.Code)
	}
	again := decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search", `{"q":"dua","mode":"fts"}`), 200)
	if again.SearchID == "" || again.SearchID == sid || again.TranscriptTotal != 1 {
		t.Fatalf("re-run: %+v", again)
	}
}

type recentList struct {
	Items []RecentSearch `json:"items"`
}

// Recent searches: per user, newest first, the same query and mode once,
// bounded, with live / expired; remove one or all.
func TestRecentSearches(t *testing.T) {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.library(libA, "L")
	f.video("va", libA, "Salah times", 60)
	f.transcript("ta", "va", true, "salah", "salah", "times")
	c := &clock{t: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	h := router(&Handler{DB: db, NowFunc: c.now})
	search := func(p *principal.Principal, q, mode string) string {
		c.t = c.t.Add(time.Minute)
		return decode[Response](t, call(t, h, p, http.MethodPost, "/api/search",
			`{"q":"`+q+`","mode":"`+mode+`"}`), 200).SearchID
	}
	search(admin, "salah", "fts")
	search(admin, "times", "fts")
	last := search(admin, "salah", "fts") // replaces the first
	search(adminB, "other user", "fts")
	if sid := decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search",
		`{"q":"lookup","mode":"fts","save":false}`), 200).SearchID; sid != "" {
		t.Fatalf("save:false kept %q", sid)
	}

	got := decode[recentList](t, call(t, h, admin, http.MethodGet, "/api/search/recent", ""), 200).Items
	if len(got) != 2 || got[0].SearchID != last || got[0].Q != "salah" || got[1].Q != "times" {
		t.Fatalf("recent %+v", got)
	}
	if !got[0].Live || got[0].TitleCount != 1 || got[0].TranscriptCount != 1 || got[0].Mode != "fts" {
		t.Fatalf("entry %+v", got[0])
	}
	// opening a live one by id gives the same result set
	page := decode[resultsPage](t, call(t, h, admin, http.MethodGet, "/api/search/results/"+last+"?list=transcripts", ""), 200)
	if page.Total != 1 || page.Items[0].VideoID != "va" || page.Items[0].Matches != 2 {
		t.Fatalf("open live %+v", page)
	}
	// expired: listed, not live, its results gone
	c.t = c.t.Add(time.Hour)
	got = decode[recentList](t, call(t, h, admin, http.MethodGet, "/api/search/recent", ""), 200).Items
	if len(got) != 2 || got[0].Live || got[1].Live {
		t.Fatalf("after an hour %+v", got)
	}
	// another user's entry cannot be removed through this user
	other := decode[recentList](t, call(t, h, adminB, http.MethodGet, "/api/search/recent", ""), 200).Items
	if len(other) != 1 || other[0].Q != "other user" {
		t.Fatalf("user B %+v", other)
	}
	if rec := call(t, h, admin, http.MethodDelete, "/api/search/recent/"+other[0].SearchID, ""); rec.Code != 404 {
		t.Fatalf("deleted another user's: %d", rec.Code)
	}
	if rec := call(t, h, admin, http.MethodDelete, "/api/search/recent/"+last, ""); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	got = decode[recentList](t, call(t, h, admin, http.MethodGet, "/api/search/recent", ""), 200).Items
	if len(got) != 1 || got[0].Q != "times" {
		t.Fatalf("after delete %+v", got)
	}
	if rec := call(t, h, admin, http.MethodDelete, "/api/search/recent", ""); rec.Code != 204 {
		t.Fatalf("clear: %d", rec.Code)
	}
	if got = decode[recentList](t, call(t, h, admin, http.MethodGet, "/api/search/recent", ""), 200).Items; len(got) != 0 {
		t.Fatalf("after clear %+v", got)
	}
	if other = decode[recentList](t, call(t, h, adminB, http.MethodGet, "/api/search/recent", ""), 200).Items; len(other) != 1 {
		t.Fatalf("clear reached user B: %+v", other)
	}
}

// The same search run again (another tab, or after an expiry) leaves the
// first one's results readable while they are kept; Recent shows it once.
func TestSearchID_SameSearchTwiceKeepsBoth(t *testing.T) {
	db := searchDB(t)
	c := &clock{t: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	h := router(&Handler{DB: db, NowFunc: c.now})
	first := decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search", `{"q":"x","mode":"fts"}`), 200).SearchID
	c.t = c.t.Add(time.Minute)
	second := decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search", `{"q":"x","mode":"fts"}`), 200).SearchID
	for _, sid := range []string{first, second} {
		decode[resultsPage](t, call(t, h, admin, http.MethodGet, "/api/search/results/"+sid+"?list=titles", ""), 200)
	}
	got := decode[recentList](t, call(t, h, admin, http.MethodGet, "/api/search/recent", ""), 200).Items
	if len(got) != 1 || got[0].SearchID != second {
		t.Fatalf("recent %+v", got)
	}
	// both expired, then the search again: the old rows go
	c.t = c.t.Add(time.Hour)
	decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search", `{"q":"x","mode":"fts"}`), 200)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM search_runs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows %d %v", n, err)
	}
}

// The history keeps the newest rules.search.recent_limit searches.
func TestRecentSearches_Bounded(t *testing.T) {
	db := searchDB(t)
	// a limit of 3 (the default is 20): five searches prove the bound, at a fifth of
	// the cost of 25 (each search is a write; the unit budget is per test)
	if _, err := db.Exec(`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT, updated_by TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ('rules.search', '{"recent_limit": 3}')`); err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	h := router(&Handler{DB: db, NowFunc: c.now})
	for i := 0; i < 5; i++ {
		c.t = c.t.Add(time.Second)
		decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search", fmt.Sprintf(`{"q":"q%d","mode":"fts"}`, i)), 200)
	}
	got := decode[recentList](t, call(t, h, admin, http.MethodGet, "/api/search/recent", ""), 200).Items
	if len(got) != 3 || got[0].Q != "q4" || got[2].Q != "q2" {
		t.Fatalf("recent %d: first %q", len(got), got[0].Q)
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM search_runs`).Scan(&rows); err != nil || rows != 3 {
		t.Fatalf("rows kept %d %v", rows, err)
	}
}

// fixedSemantic answers the same passages.
type fixedSemantic struct{ hits []Hit }

func (s fixedSemantic) Search(context.Context, string, int, Filters) ([]Hit, error) {
	return append([]Hit(nil), s.hits...), nil
}

// Semantic groups the meaning search's passages per video; hybrid fuses the
// two video rankings and counts a passage both legs found once.
func TestSearch_SemanticAndHybridGrouping(t *testing.T) {
	db := searchDB(t)
	f := &fixture{t: t, db: db}
	f.library(libA, "L")
	f.video("va", libA, "A", 60)
	f.video("vb", libA, "B", 60)
	f.video("vc", libA, "C", 60)
	f.transcript("ta", "va", true, "mercy", "mercy", "kindness")       // segments 1-3
	f.transcript("tb", "vb", true, "compassion", "gentle", "softness") // 4-6
	f.transcript("tc", "vc", true, "mercy")                            // 7
	sem := fixedSemantic{hits: []Hit{
		{SegmentID: 4, VideoID: "vb", StartSec: 0, EndSec: 9, Snippet: "compassion", Score: 0.9},
		{SegmentID: 5, VideoID: "vb", StartSec: 10, EndSec: 19, Snippet: "gentle", Score: 0.8},
		{SegmentID: 1, VideoID: "va", StartSec: 0, EndSec: 9, Snippet: "mercy", Score: 0.7},
		{SegmentID: 3, VideoID: "va", StartSec: 20, EndSec: 29, Snippet: "kindness", Score: 0.6},
	}}
	h := router(&Handler{DB: db, Semantic: sem})

	resp := decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search", `{"q":"mercy","mode":"semantic"}`), 200)
	got := map[string]int{}
	for _, v := range resp.TranscriptVideos {
		got[v.VideoID] = v.Matches
	}
	if ids := videoIDs(resp.TranscriptVideos, func(v TranscriptVideo) string { return v.VideoID }); !reflect.DeepEqual(ids, []string{"vb", "va"}) ||
		got["vb"] != 2 || got["va"] != 2 {
		t.Fatalf("semantic %v %v", ids, got)
	}

	resp = decode[Response](t, call(t, h, admin, http.MethodPost, "/api/search", `{"q":"mercy","mode":"hybrid"}`), 200)
	got = map[string]int{}
	for _, v := range resp.TranscriptVideos {
		if _, dup := got[v.VideoID]; dup {
			t.Fatalf("%s twice", v.VideoID)
		}
		got[v.VideoID] = v.Matches
	}
	// va: FTS 2 (segments 1, 2) + semantic-only segment 3 = 3 (segment 1 once)
	if got["va"] != 3 || got["vb"] != 2 || got["vc"] != 1 || resp.TranscriptVideos[0].VideoID != "va" {
		t.Fatalf("hybrid %+v", resp.TranscriptVideos)
	}
	all := decode[passagesPage](t, call(t, h, admin, http.MethodGet, "/api/search/passages?sid="+resp.SearchID+"&video_id=va&order=time", ""), 200)
	if all.Total != 3 || all.Items[0].SegmentID != 1 || all.Items[2].SegmentID != 3 {
		t.Fatalf("hybrid passages %+v", all.Items)
	}
}

func TestFuseVideos_CountsSharedPassagesOnce(t *testing.T) {
	fts := []savedVideo{{V: "a", S: 0.5, N: 10}, {V: "b", S: 0.4, N: 1}}
	sem := []Hit{{SegmentID: 1, VideoID: "b", Score: 0.9}, {SegmentID: 2, VideoID: "a", Score: 0.5}, {SegmentID: 3, VideoID: "c", Score: 0.4}}
	got := fuseVideos(fts, sem, map[int64]bool{2: true}, 1, 1)
	n := map[string]int{}
	for _, v := range got {
		n[v.V] = v.N
	}
	if n["a"] != 10 || n["b"] != 2 || n["c"] != 1 || len(got) != 3 {
		t.Fatalf("fused %+v", got)
	}
	// a and b each rank once first, once second: a tie broken by matches
	if got[0].V != "a" || got[1].V != "b" || got[2].V != "c" {
		t.Fatalf("order %+v", got)
	}
}

// The API's defaults are the schema's (pipeline search_settings.py).
func TestRules_DefaultsMatchSchema(t *testing.T) {
	m, err := rules.Effective(context.Background(), nil, "search")
	if err != nil {
		t.Fatal(err)
	}
	got := DefaultRules()
	applyRuleInts(m, map[string]*int{
		"passages_per_video": &got.PassagesPerVideo, "page_size_grid": &got.PageSizeGrid,
		"page_size_list": &got.PageSizeList, "passages_page_size": &got.PassagesPageSize,
		"max_videos": &got.MaxVideos, "semantic_top_k": &got.SemanticTopK,
		"result_minutes": &got.ResultMinutes, "recent_limit": &got.RecentLimit,
	})
	if got != DefaultRules() || len(m) != 8 {
		t.Fatalf("schema %v, Go %+v", m, DefaultRules())
	}
}

// A stored rule changes the page size and the passages per video.
func TestRules_StoredOverrides(t *testing.T) {
	db := searchDB(t)
	if _, err := db.Exec(`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT, updated_by TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ('rules.search', '{"page_size_list": 5, "passages_per_video": 1, "result_minutes": 0}')`); err != nil {
		t.Fatal(err)
	}
	got := (&Handler{DB: db}).rules(context.Background())
	if got.PageSizeList != 5 || got.PassagesPerVideo != 1 || got.ResultMinutes != 15 {
		t.Fatalf("rules %+v (result_minutes 0 is out of range: the default stays)", got)
	}
}
