package enrichment

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

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

// A minimal SQLite shape of the tables these handlers touch (slots 0077,
// 0078, 0079, 0139).
const testSchema = `
CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT, filename TEXT, title TEXT,
  description TEXT, poster_path TEXT, updated_at TIMESTAMP);
CREATE TABLE media_metadata_enrichment (id TEXT PRIMARY KEY, video_id TEXT, provider TEXT,
  external_id TEXT, mapped TEXT, confidence REAL, is_accepted BOOLEAN DEFAULT false,
  is_dismissed BOOLEAN DEFAULT false, UNIQUE (video_id, provider, external_id));
CREATE TABLE media_field_provenance (video_id TEXT, field TEXT, origin TEXT, prev_value TEXT,
  source_id TEXT, set_at TIMESTAMP, PRIMARY KEY (video_id, field));
CREATE TABLE enrichment_decisions (id TEXT PRIMARY KEY, video_id TEXT, external_id TEXT,
  action TEXT, actor_user_id TEXT, applied TEXT, skipped TEXT, created_at TIMESTAMP);
CREATE TABLE enrich_jobs (id TEXT PRIMARY KEY, video_id TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending', force BOOLEAN NOT NULL DEFAULT false,
  attempts INTEGER NOT NULL DEFAULT 0, not_before TIMESTAMP, last_error TEXT,
  created_at TIMESTAMP, updated_at TIMESTAMP, query TEXT, year INTEGER,
  origin TEXT NOT NULL DEFAULT 'auto');
CREATE UNIQUE INDEX enrich_jobs_open_video_idx ON enrich_jobs (video_id)
  WHERE status IN ('pending','running','deferred');
`

type enqueued struct {
	mu    sync.Mutex
	calls []string
}

func (e *enqueued) fn(_ context.Context, videoID string, reason map[string]any) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, videoID+":"+asString(reason["origin"]))
	return nil
}

func sqliteHandler(t *testing.T) (*Handler, *sql.DB, *enqueued, chi.Router) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range strings.Split(testSchema, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%v: %s", err, stmt)
		}
	}
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO videos (id, library_id, filename, title, updated_at)
		VALUES ($1, 'lib', 'bulugh_03.mp4', 'Lesson 3', $2)`, okID, at); err != nil {
		t.Fatal(err)
	}
	e := &enqueued{}
	h := &Handler{DB: db, Enqueue: e.fn, NowFunc: func() time.Time { return at.Add(time.Hour) }}
	r := chi.NewRouter()
	h.Mount(r)
	return h, db, e, r
}

func call(r chi.Router, method, path, body string, admin bool) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req(method, path, body, admin))
	return rec
}

func TestSearchQueuesTheProviderWithTheQuery(t *testing.T) {
	_, db, e, r := sqliteHandler(t)
	rec := call(r, http.MethodPost, "/api/videos/"+okID+"/enrichment/search",
		`{"query":"  بلوغ المرام  ","year":2019}`, true)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Queued     bool        `json:"queued"`
		Candidates []Candidate `json:"candidates"`
		Job        *JobStatus  `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Queued || out.Candidates == nil || out.Job == nil {
		t.Fatalf("body: %s", rec.Body.String())
	}
	if out.Job.Status != "pending" || out.Job.Origin != "manual" || out.Job.Query != "بلوغ المرام" ||
		out.Job.Year == nil || *out.Job.Year != 2019 {
		t.Fatalf("job = %+v", out.Job)
	}
	if len(e.calls) != 1 || e.calls[0] != okID+":manual" {
		t.Fatalf("engine enqueue = %v", e.calls)
	}

	// a second search while the first still waits re-arms the same row
	rec = call(r, http.MethodPost, "/api/videos/"+okID+"/enrichment/search", `{"query":"other words"}`, true)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d", rec.Code)
	}
	var n int
	var q string
	_ = db.QueryRow(`SELECT count(*), max(query) FROM enrich_jobs WHERE video_id = $1`, okID).Scan(&n, &q)
	if n != 1 || q != "other words" {
		t.Fatalf("rows=%d query=%q", n, q)
	}

	// running: no second open row; the answer says so
	_, _ = db.Exec(`UPDATE enrich_jobs SET status = 'running'`)
	rec = call(r, http.MethodPost, "/api/videos/"+okID+"/enrichment/search", `{"query":"x"}`, true)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "is running") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSearchValidatesAndNeedsAnEditor(t *testing.T) {
	_, _, e, r := sqliteHandler(t)
	long := strings.Repeat("ب", 201)
	if rec := call(r, http.MethodPost, "/api/videos/"+okID+"/enrichment/search", `{"query":"`+long+`"}`, true); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("long query: %d", rec.Code)
	}
	if rec := call(r, http.MethodPost, "/api/videos/"+okID+"/enrichment/search", `{"query":"x","year":12}`, true); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad year: %d", rec.Code)
	}
	if rec := call(r, http.MethodPost, "/api/videos/"+okID+"/enrichment/search", `{"query":"x"}`, false); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer: %d", rec.Code)
	}
	if rec := call(r, http.MethodPost, "/api/videos/22222222-2222-2222-2222-222222222222/enrichment/search", `{}`, true); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown video: %d", rec.Code)
	}
	if len(e.calls) != 0 {
		t.Fatalf("nothing may be queued: %v", e.calls)
	}
}

func TestReenrichQueuesAndEnqueues(t *testing.T) {
	_, db, e, r := sqliteHandler(t)
	rec := call(r, http.MethodPost, "/api/videos/"+okID+"/enrichment/reenrich", "", true)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var origin string
	var force bool
	if err := db.QueryRow(`SELECT origin, force FROM enrich_jobs WHERE video_id = $1`, okID).Scan(&origin, &force); err != nil {
		t.Fatal(err)
	}
	if origin != "reenrich" || !force || len(e.calls) != 1 {
		t.Fatalf("origin=%s force=%v calls=%v", origin, force, e.calls)
	}
}

func TestListShowsSourcesFactsAndTheJob(t *testing.T) {
	_, db, _, r := sqliteHandler(t)
	mapped := `{"title":"شرح بلوغ المرام 3","year":2019,"provider":"web",
	  "fields":{"title":{"value":"شرح بلوغ المرام 3","confidence":0.9,"sources":["S1","I1"]},
	            "book_author":{"value":"ابن حجر","confidence":0.8,"sources":["S1"]},
	            "episode":{"value":3,"confidence":0.7,"sources":["I1"]}},
	  "sources":[{"id":"S1","label":"Wikipedia","url":"https://ar.wikipedia.org/x"},
	             {"id":"S2","label":"bad","url":"javascript:alert(1)"},
	             {"id":"I1","label":"File and folder names","kind":"file"}]}`
	if _, err := db.Exec(`INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, mapped, confidence)
		VALUES ('c1', $1, 'web', 'web:abc', $2, 0.9)`, okID, mapped); err != nil {
		t.Fatal(err)
	}
	_ = call(r, http.MethodPost, "/api/videos/"+okID+"/enrichment/reenrich", "", true)
	rec := call(r, http.MethodGet, "/api/videos/"+okID+"/enrichment", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Candidates []Candidate `json:"candidates"`
		Job        *JobStatus  `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Candidates) != 1 || out.Job == nil || out.Job.Origin != "reenrich" {
		t.Fatalf("body: %s", rec.Body.String())
	}
	c := out.Candidates[0]
	if len(c.Fields) != 1 || c.Fields[0].Field != "title" || c.Fields[0].Confidence == nil ||
		*c.Fields[0].Confidence != 0.9 || strings.Join(c.Fields[0].Sources, ",") != "S1,I1" {
		t.Fatalf("fields = %+v", c.Fields)
	}
	if len(c.Facts) != 2 || c.Facts[0].Field != "episode" || c.Facts[1].Field != "book_author" {
		t.Fatalf("facts = %+v", c.Facts)
	}
	if len(c.Sources) != 3 || c.Sources[0].URL != "https://ar.wikipedia.org/x" || c.Sources[1].URL != "" ||
		c.Sources[2].Kind != "file" {
		t.Fatalf("sources = %+v", c.Sources)
	}
}
