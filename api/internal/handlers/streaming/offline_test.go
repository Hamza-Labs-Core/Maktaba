package streaming

// A full node plays with the home's database down (PR #145): the video's library and
// length from the node's catalog (the person's library access still checked), the
// session's owner kept on the node, no streaming_sessions row written meanwhile.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

type catalogVideo struct {
	lib string
	dur float64
}

type fakeOffline struct {
	mu     sync.Mutex
	down   bool
	failed []error
	videos map[string]catalogVideo
}

func (f *fakeOffline) DBDown() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.down }
func (f *fakeOffline) DBFailed(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, err)
	f.down = true
}
func (f *fakeOffline) Video(id string) (string, float64, bool) {
	v, ok := f.videos[id]
	return v.lib, v.dur, ok
}

type fakeService struct {
	mu     sync.Mutex
	closed []string
}

func (s *fakeService) Open(_ context.Context, req OpenSessionRequest) (OpenSessionResponse, error) {
	return OpenSessionResponse{SessionID: uuid.NewString(), Mode: "direct", DirectURL: "/stream/direct/" + req.VideoID}, nil
}
func (s *fakeService) Close(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = append(s.closed, id)
	return nil
}
func (s *fakeService) Capabilities(context.Context) (Capabilities, error) { return Capabilities{}, nil }

const (
	vHere = "22222222-0000-4000-8000-000000000001"
	vLib2 = "22222222-0000-4000-8000-000000000002"
	vGone = "22222222-0000-4000-8000-000000000009"
)

// closedDB is the home's database gone: every call on it fails.
func closedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	return db
}

func offlineRig(t *testing.T, down bool) (http.Handler, *fakeOffline, *fakeService) {
	t.Helper()
	off := &fakeOffline{down: down, videos: map[string]catalogVideo{
		vHere: {lib: "lib1", dur: 1800}, vLib2: {lib: "lib2", dur: 60},
	}}
	svc := &fakeService{}
	h := &Handler{DB: closedDB(t), Service: svc, Offline: off}
	r := chi.NewRouter()
	h.Mount(r)
	return r, off, svc
}

func as(user string, libs ...string) *principal.Principal {
	return &principal.Principal{UserID: user, Libraries: libs, Source: principal.SourceCookie}
}

func call(h http.Handler, p *principal.Principal, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func open(t *testing.T, h http.Handler, p *principal.Principal, video string) (string, *httptest.ResponseRecorder) {
	t.Helper()
	rec := call(h, p, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+video+`"}`)
	var s OpenSessionResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	return s.SessionID, rec
}

func TestOfflinePlaybackFromTheCatalog(t *testing.T) {
	h, _, svc := offlineRig(t, true)
	amal := as("u-amal", "lib1")
	id, rec := open(t, h, amal, vHere)
	if rec.Code != http.StatusCreated || id == "" {
		t.Fatalf("open with the home's database down: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(h, amal, http.MethodGet, "/api/stream/sessions/"+id, ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), vHere) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	// the session's owner is the node's record: another user cannot close it
	if rec := call(h, as("u-other", "lib1"), http.MethodDelete, "/api/stream/sessions/"+id, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("someone else's session: %d, want 404", rec.Code)
	}
	if rec := call(h, amal, http.MethodDelete, "/api/stream/sessions/"+id, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("close: %d %s", rec.Code, rec.Body.String())
	}
	if len(svc.closed) != 1 || svc.closed[0] != id {
		t.Fatalf("the streaming session was not closed: %v", svc.closed)
	}
	if rec := call(h, amal, http.MethodDelete, "/api/stream/sessions/"+id, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("closing it again: %d, want 404", rec.Code)
	}
	if rec := call(h, amal, http.MethodDelete, "/api/stream/sessions/"+uuid.NewString(), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown session: %d, want 404", rec.Code)
	}
}

func TestOfflinePlaybackKeepsLibraryAccess(t *testing.T) {
	h, _, _ := offlineRig(t, true)
	if _, rec := open(t, h, as("u-amal", "lib1"), vLib2); rec.Code != http.StatusForbidden {
		t.Fatalf("a video of a library the person may not see: %d, want 403", rec.Code)
	}
	admin := &principal.Principal{UserID: "u-admin", IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceCookie}
	if _, rec := open(t, h, admin, vLib2); rec.Code != http.StatusCreated {
		t.Fatalf("an admin: %d", rec.Code)
	}
}

func TestOfflineAVideoOutsideTheCatalogIsUnavailable(t *testing.T) {
	h, _, _ := offlineRig(t, true)
	_, rec := open(t, h, as("u-amal", "lib1"), vGone)
	var p struct{ Type, Code string }
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if rec.Code != http.StatusServiceUnavailable || p.Type != ProblemUnavailableOffline || p.Code != "video_unavailable_offline" {
		t.Fatalf("a video the catalog lacks: %d %s", rec.Code, rec.Body.String())
	}
}

// A database not yet known to be down: its failure is recorded and the catalog answers.
func TestOfflineAFailingDatabaseFallsBack(t *testing.T) {
	h, off, _ := offlineRig(t, false)
	if _, rec := open(t, h, as("u-amal", "lib1"), vHere); rec.Code != http.StatusCreated {
		t.Fatalf("open on a failing database: %d %s", rec.Code, rec.Body.String())
	}
	if len(off.failed) == 0 || errors.Is(off.failed[0], sql.ErrNoRows) {
		t.Fatalf("the failure was not recorded: %v", off.failed)
	}
}

// The home has no catalog: a database failure stays an error there.
func TestOnTheHomeADatabaseFailureIsAnError(t *testing.T) {
	h := &Handler{DB: closedDB(t), Service: &fakeService{}}
	r := chi.NewRouter()
	h.Mount(r)
	if _, rec := open(t, r, as("u-amal", "lib1"), vHere); rec.Code != http.StatusInternalServerError {
		t.Fatalf("the home with its database failing: %d, want 500", rec.Code)
	}
}
