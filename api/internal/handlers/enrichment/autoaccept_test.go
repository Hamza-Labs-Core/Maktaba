package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

const (
	vidA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	vidB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	vidC = "cccccccc-cccc-cccc-cccc-cccccccccccc"
)

// queueDB is a SQLite database with the columns the review queue touches,
// three videos and one waiting candidate each:
//
//	A: confidence 0.95, no user edits
//	B: confidence 0.60
//	C: confidence 0.97, but the user edited its title
func queueDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "enrich.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// One connection: the sweep's transaction and the test's reads take
	// turns instead of racing for SQLite's write lock (SQLITE_BUSY).
	db.SetMaxOpenConns(1)
	stmts := []string{
		`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT, filename TEXT, title TEXT,
			description TEXT, poster_path TEXT, updated_at TIMESTAMP)`,
		`CREATE TABLE media_metadata_enrichment (id TEXT PRIMARY KEY, video_id TEXT, provider TEXT,
			external_id TEXT, confidence REAL, is_accepted BOOLEAN DEFAULT false,
			is_dismissed BOOLEAN DEFAULT false, mapped TEXT)`,
		`CREATE TABLE media_field_provenance (video_id TEXT, field TEXT, origin TEXT, prev_value TEXT,
			source_id TEXT, set_at TIMESTAMP, PRIMARY KEY (video_id, field))`,
		`CREATE TABLE enrichment_decisions (id TEXT PRIMARY KEY, video_id TEXT, external_id TEXT,
			action TEXT, actor_user_id TEXT, applied TEXT, skipped TEXT, created_at TIMESTAMP)`,
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []struct {
		id, title, ext string
		conf           float64
	}{{vidA, "a.mp4", "ext-a", 0.95}, {vidB, "b.mp4", "ext-b", 0.60}, {vidC, "My own title", "ext-c", 0.97}} {
		if _, err := db.Exec(`INSERT INTO videos VALUES ($1,'lib',$2,$3,NULL,NULL,$4)`, v.id, v.title, v.title, now); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, confidence, mapped)
			VALUES ($1,$2,'tmdb',$3,$4,$5)`, "c-"+v.ext, v.id, v.ext, v.conf,
			`{"title":"Proposed `+v.ext+`","description":"About `+v.ext+`"}`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO media_field_provenance VALUES ($1,'title','user',NULL,NULL,$2)`, vidC, now); err != nil {
		t.Fatal(err)
	}
	return db
}

func setThreshold(t *testing.T, db *sql.DB, raw string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1,$2)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, AutoAcceptSettingsKey, raw); err != nil {
		t.Fatal(err)
	}
}

func title(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var s sql.NullString
	if err := db.QueryRow(`SELECT title FROM videos WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s.String
}

func TestLoadAutoAcceptThreshold(t *testing.T) {
	db := queueDB(t)
	ctx := context.Background()
	if got, err := LoadAutoAcceptThreshold(ctx, db); err != nil || got != 0 {
		t.Fatalf("no row: got %v, %v; want 0 (off)", got, err)
	}
	for raw, want := range map[string]float64{
		`{"auto_accept_threshold":0.9}`: 0.9,
		`{"auto_accept_threshold":0}`:   0,
		`{"auto_accept_threshold":1.5}`: 0,
		`{"auto_accept_threshold":-1}`:  0,
		`{}`:                            0,
		`not json`:                      0,
	} {
		setThreshold(t, db, raw)
		if got, err := LoadAutoAcceptThreshold(ctx, db); err != nil || got != want {
			t.Errorf("%s: got %v, %v; want %v", raw, got, err, want)
		}
	}
}

func TestAutoAcceptOffByDefault(t *testing.T) {
	db := queueDB(t)
	h := &Handler{DB: db}
	res, err := h.AutoAcceptOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Accepted) != 0 || title(t, db, vidA) != "a.mp4" {
		t.Fatalf("default threshold accepted %v; want nothing", res.Accepted)
	}
}

func TestAutoAcceptAboveThreshold(t *testing.T) {
	db := queueDB(t)
	setThreshold(t, db, `{"auto_accept_threshold":0.9}`)
	h := &Handler{DB: db}
	res, err := h.AutoAcceptOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Accepted) != 2 {
		t.Fatalf("accepted %d videos, want 2 (A and C): %+v", len(res.Accepted), res.Accepted)
	}
	if got := title(t, db, vidA); got != "Proposed ext-a" {
		t.Errorf("A title = %q, want the proposed one", got)
	}
	if got := title(t, db, vidB); got != "b.mp4" {
		t.Errorf("B (below threshold) title = %q, want unchanged", got)
	}
	if got := title(t, db, vidC); got != "My own title" {
		t.Errorf("C title = %q: a user edit must never be replaced", got)
	}
	var action string
	if err := db.QueryRow(`SELECT action FROM enrichment_decisions WHERE video_id = $1`, vidA).Scan(&action); err != nil || action != "auto_accept" {
		t.Errorf("decision = %q, %v; want auto_accept", action, err)
	}

	// A second sweep finds nothing left to accept.
	again, err := h.AutoAcceptOnce(context.Background())
	if err != nil || len(again.Accepted) != 0 {
		t.Fatalf("second sweep accepted %v, %v; want nothing", again.Accepted, err)
	}

	// Revert to original restores the pre-accept value.
	r := chi.NewRouter()
	h.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req(http.MethodPost, "/api/videos/"+vidA+"/enrichment/revert", `{}`, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("revert: %d %s", rec.Code, rec.Body.String())
	}
	if got := title(t, db, vidA); got != "a.mp4" {
		t.Errorf("after revert A title = %q, want the original", got)
	}
}

func TestAutoAcceptSkipsAlreadyAcceptedVideo(t *testing.T) {
	db := queueDB(t)
	setThreshold(t, db, `{"auto_accept_threshold":0.5}`)
	if _, err := db.Exec(`INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, confidence, is_accepted, mapped)
		VALUES ('c-old', $1, 'tmdb', 'ext-old', 0.4, true, '{}')`, vidB); err != nil {
		t.Fatal(err)
	}
	res, err := (&Handler{DB: db}).AutoAcceptOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range res.Accepted {
		if a.VideoID == vidB {
			t.Fatalf("B already has an accepted match; the sweep must leave it alone")
		}
	}
}

func TestRunAutoAcceptStopsWithContext(t *testing.T) {
	db := queueDB(t)
	setThreshold(t, db, `{"auto_accept_threshold":0.9}`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		(&Handler{DB: db}).RunAutoAccept(ctx, time.Hour, nil)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for title(t, db, vidA) != "Proposed ext-a" {
		if time.Now().After(deadline) {
			t.Fatal("the first sweep did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunAutoAccept did not stop")
	}
}

func TestListReportsRevertableFields(t *testing.T) {
	db := queueDB(t)
	setThreshold(t, db, `{"auto_accept_threshold":0.9}`)
	h := &Handler{DB: db}
	r := chi.NewRouter()
	h.Mount(r)
	list := func() map[string]any {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req(http.MethodGet, "/api/videos/"+vidA+"/enrichment", "", true))
		if rec.Code != http.StatusOK {
			t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := list()["revertable"].([]any); len(got) != 0 {
		t.Fatalf("before accept revertable = %v, want none", got)
	}
	if _, err := h.AutoAcceptOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := list()["revertable"].([]any)
	if len(got) != 2 || got[0] != "title" || got[1] != "description" {
		t.Fatalf("after accept revertable = %v, want [title description]", got)
	}
}
