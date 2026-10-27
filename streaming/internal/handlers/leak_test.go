package handlers

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
)

// noLeak fails when a response body carries a server path or a Go error's words.
func noLeak(t *testing.T, body string, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(body, s) {
			t.Errorf("the response carries %q: %s", s, body)
		}
	}
}

// A missing poster answers 404 without the cache's path; a file the server cannot
// read is logged with its path and error, which the response leaves out.
func TestStaticHandler_NeverSendsThePathOrError(t *testing.T) {
	dir := t.TempDir()
	h := &StaticHandler{Resolver: &fakeResolver{root: dir}, Files: OSFileOpener{}}
	r := chi.NewRouter()
	r.Get("/stream/posters/{video_id}", h.ServePoster)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/posters/missing.jpg", nil))
	if rec.Code != 404 {
		t.Fatalf("status %d", rec.Code)
	}
	noLeak(t, rec.Body.String(), dir, "posters/")

	var buf bytes.Buffer
	h = &StaticHandler{Resolver: &fakeResolver{root: dir}, Files: failingOpener{fs.ErrPermission},
		Logger: slog.New(slog.NewTextHandler(&buf, nil))}
	r = chi.NewRouter()
	r.Get("/stream/posters/{video_id}", h.ServePoster)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/posters/vid.jpg", nil))
	if rec.Code != 404 {
		t.Fatalf("status %d", rec.Code)
	}
	noLeak(t, rec.Body.String(), dir, "permission denied")
	if !strings.Contains(buf.String(), "event=static_asset_read_failed") || !strings.Contains(buf.String(), "permission denied") {
		t.Errorf("an unreadable asset was not logged: %s", buf.String())
	}
}

type failingChapterReader struct{ err error }

func (f failingChapterReader) List(context.Context, string) ([]Chapter, error) { return nil, f.err }

// A failed chapters read is logged; the player gets the problem without the database's words.
func TestChapterHandler_DatabaseErrorIsLoggedNotSent(t *testing.T) {
	var buf bytes.Buffer
	h := &ChapterHandler{Reader: failingChapterReader{errors.New("pq: relation \"chapters\" does not exist")},
		Resolve: func(_ context.Context, s string) (string, error) { return s, nil },
		Logger:  slog.New(slog.NewTextHandler(&buf, nil))}
	r := chi.NewRouter()
	sess := uuid.New().String()
	r.With(injectSubject(sess)).Get("/stream/{session_id}/chapters.json", h.ServeJSON)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/"+sess+"/chapters.json", nil))
	if rec.Code != 500 {
		t.Fatalf("status %d", rec.Code)
	}
	noLeak(t, rec.Body.String(), "relation", "pq:")
	if !strings.Contains(buf.String(), "event=chapters_read_failed") || !strings.Contains(buf.String(), "relation") {
		t.Errorf("not logged: %s", buf.String())
	}
}

type failingBackend struct{ err error }

func (f failingBackend) Lookup(context.Context, uuid.UUID) (*probe.Row, error) { return nil, f.err }

// A video the database does not know is a 404; a database that does not answer is a
// 503 the player may retry, logged, its error never in the response.
func TestDirect_LookupFailureIs503NotNotFound(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		err    error
		status int
		logged bool
	}{
		{probe.ErrNotFound, 404, false},
		{errors.New("failed to connect to `host=10.0.0.5 user=maktaba`: dial tcp: refused"), 503, true},
	} {
		var buf bytes.Buffer
		h := &DirectHandler{Probe: probe.NewCache(failingBackend{tc.err}, 4), Profiles: capability.NewRegistry(),
			Files: &memoryOpener{}, AllowAllProfiles: true, Logger: slog.New(slog.NewTextHandler(&buf, nil))}
		r := chi.NewRouter()
		r.With(injectSubject(id.String())).Get("/stream/direct/{video_id}", h.ServeHTTP)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/direct/"+id.String(), nil))
		if rec.Code != tc.status {
			t.Errorf("%v: status %d, want %d", tc.err, rec.Code, tc.status)
		}
		noLeak(t, rec.Body.String(), "10.0.0.5", "dial tcp")
		if got := strings.Contains(buf.String(), "event=vod_direct_lookup_failed"); got != tc.logged {
			t.Errorf("%v: logged %v, want %v: %s", tc.err, got, tc.logged, buf.String())
		}
	}
}
