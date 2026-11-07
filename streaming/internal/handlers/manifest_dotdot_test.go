package handlers

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

// TestManifestHandler_RefusesDotDotSegments: a rendition or segment name with ".."
// reads nothing outside the session's folder, a sibling session's files among them
// (CodeQL go/path-injection, alerts 89 and 105).
func TestManifestHandler_RefusesDotDotSegments(t *testing.T) {
	dir := t.TempDir()
	layout := cache.New(dir)
	_ = layout.EnsureTiers()
	sessID, other := uuid.New(), uuid.New()
	otherDir := filepath.Join(layout.HLSDir(other.String()), "v0")
	_ = os.MkdirAll(otherDir, 0o755)
	_ = os.MkdirAll(filepath.Join(layout.HLSDir(sessID.String()), "v0"), 0o755)
	for _, f := range []string{"seg-0.ts", "index.m3u8"} {
		if err := os.WriteFile(filepath.Join(otherDir, f), []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store := &fakeSessionStore{rows: map[uuid.UUID]*session.Row{sessID: {ID: sessID, Format: session.FormatHLS}}}
	h := &ManifestHandler{Sessions: store, Layout: layout, Now: func() time.Time { return time.Unix(123, 0) }}
	r := chi.NewRouter()
	r.With(injectSubject(sessID.String())).Get("/stream/{session_id}/{rendition}/index.m3u8", h.ServeRenditionIndex)
	r.With(injectSubject(sessID.String())).Get("/stream/{session_id}/{rendition}/{segment}", h.ServeSegment)
	for _, path := range []string{
		"/stream/" + sessID.String() + "/..%2F" + other.String() + "%2Fv0/index.m3u8",
		"/stream/" + sessID.String() + "/..%2F" + other.String() + "%2Fv0/seg-0.ts",
		"/stream/" + sessID.String() + "/v0/..%2F..%2F" + other.String() + "%2Fv0%2Fseg-0.ts",
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 404 || rec.Body.String() == "secret" {
			t.Errorf("%s: %d %q, want 404", path, rec.Code, rec.Body.String())
		}
	}
}
