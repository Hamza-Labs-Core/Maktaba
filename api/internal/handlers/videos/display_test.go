package videos

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

const vid = "11111111-1111-1111-1111-111111111111"

func newDisplayEnv(t *testing.T) (*sql.DB, http.Handler, *[]string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, ddl := range []string{
		`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT NOT NULL, path TEXT NOT NULL,
			filename TEXT NOT NULL, title TEXT, description TEXT, state TEXT NOT NULL,
			detected_language TEXT, duration_sec REAL, size_bytes INTEGER NOT NULL DEFAULT 0,
			content_hash TEXT NOT NULL DEFAULT '', metadata TEXT, created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL, display_title TEXT, display_title_source TEXT,
			display_title_manual TEXT)`,
		`CREATE TABLE tags (id TEXT PRIMARY KEY, name TEXT, name_norm TEXT UNIQUE)`,
		`CREATE TABLE video_tags (video_id TEXT, tag_id TEXT, PRIMARY KEY (video_id, tag_id))`,
		`CREATE TABLE transcripts (id TEXT, video_id TEXT, is_active BOOLEAN, created_at DATETIME)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO videos (id, library_id, path, filename, title, state, created_at,
		updated_at, display_title, display_title_source) VALUES ($1, 'lib1', '/x/a.mp4', 'a.mp4',
		'في الخنادق… لا يوجد ملحدون! ｜ برنامج من زاوية أخرى ｜ الحلقة 45', 'ready', $2, $2,
		'في الخنادق… لا يوجد ملحدون!', 'rules')`, vid, now); err != nil {
		t.Fatal(err)
	}
	var queued []string
	h := &Handler{DB: db, DisplayTitleRefresh: func(_ context.Context, lib string) error {
		queued = append(queued, lib)
		return nil
	}}
	r := chi.NewRouter()
	h.Mount(r)
	return db, r, &queued
}

func call(t *testing.T, srv http.Handler, method, body string) (*httptest.ResponseRecorder, Video) {
	t.Helper()
	req := httptest.NewRequest(method, "/api/videos/"+vid, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	p := &principal.Principal{UserID: "a", IsAdmin: true, AccessAllLibraries: true}
	req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var v Video
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	return rec, v
}

func TestDisplayTitleDetailAndManualOverride(t *testing.T) {
	_, srv, queued := newDisplayEnv(t)
	rec, v := call(t, srv, "GET", "")
	if rec.Code != 200 || v.DisplayTitle == nil || *v.DisplayTitle != "في الخنادق… لا يوجد ملحدون!" ||
		v.DisplayTitleSource != "rules" || v.DisplayTitleManual != nil {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}
	rec, v = call(t, srv, "PATCH", `{"display_title":"  لا ملحدين في الخنادق  "}`)
	if rec.Code != 200 || *v.DisplayTitle != "لا ملحدين في الخنادق" || v.DisplayTitleSource != "manual" ||
		*v.DisplayTitleAuto != "في الخنادق… لا يوجد ملحدون!" {
		t.Fatalf("set: %d %s", rec.Code, rec.Body.String())
	}
	if len(*queued) != 1 || (*queued)[0] != "lib1" {
		t.Fatalf("refresh queued %v", *queued)
	}
	// "" (or null) goes back to the automatic title
	for _, clear := range []string{`{"display_title":""}`, `{"display_title":null}`} {
		rec, v = call(t, srv, "PATCH", clear)
		if rec.Code != 200 || v.DisplayTitleManual != nil || v.DisplayTitleSource != "rules" {
			t.Fatalf("%s: %d %s", clear, rec.Code, rec.Body.String())
		}
	}
	// other fields alone do not queue a refresh
	_, _ = call(t, srv, "PATCH", `{"description":"x"}`)
	if len(*queued) != 3 {
		t.Fatalf("queued %v", *queued)
	}
}

func TestDisplayTitleRejected(t *testing.T) {
	_, srv, queued := newDisplayEnv(t)
	for _, bad := range []string{
		`{"display_title":42}`,
		`{"display_title":"` + strings.Repeat("ح", 201) + `"}`,
		`{"display_title":"a\u0000b"}`,
		`{"display_title":"line\nbreak"}`,
	} {
		if rec, _ := call(t, srv, "PATCH", bad); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
	if len(*queued) != 0 {
		t.Fatalf("queued %v", *queued)
	}
}
