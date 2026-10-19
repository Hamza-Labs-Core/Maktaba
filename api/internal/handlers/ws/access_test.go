package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

const (
	accessLib   = "00000000-0000-4000-8000-00000000a000"
	accessVideo = "00000000-0000-4000-8000-0000000a0001"
)

func asMember(r *http.Request) *http.Request {
	return r.WithContext(principal.WithPrincipal(r.Context(),
		&principal.Principal{UserID: "m", Libraries: []string{accessLib}}))
}

// A library or video stream of a library the caller cannot read is 404,
// before any stream opens.
func TestSSE_LibraryAndPlaybackNeedTheLibrary(t *testing.T) {
	h := &Handler{Hub: NewHub(), VideoLibrary: func(_ context.Context, id string) (string, error) {
		if id == accessVideo {
			return "00000000-0000-4000-8000-00000000b000", nil // another library
		}
		return "", nil
	}}
	r := chi.NewRouter()
	h.Mount(r)
	for _, path := range []string{
		"/ws/library/00000000-0000-4000-8000-00000000b000",
		"/ws/playback/" + accessVideo,
		"/ws/playback/00000000-0000-4000-8000-0000000a0009", // unknown video
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, asMember(httptest.NewRequest("GET", path, nil)))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s -> %d, want 404", path, rec.Code)
		}
	}
	// no resolver wired: a library-scoped caller cannot open a playback stream
	r2 := chi.NewRouter()
	(&Handler{Hub: NewHub()}).Mount(r2)
	rec := httptest.NewRecorder()
	r2.ServeHTTP(rec, asMember(httptest.NewRequest("GET", "/ws/playback/"+accessVideo, nil)))
	if rec.Code != http.StatusNotFound {
		t.Errorf("playback without a resolver -> %d", rec.Code)
	}
}

// The Filter hook decides per subscriber what of a live event it sees.
func TestSSE_FilterDropsHiddenEvents(t *testing.T) {
	hub := NewHub()
	h := &Handler{Hub: hub, Filter: func(_ *principal.Principal, _ string, e Event) (Event, bool) {
		return e, e.Type != "hidden"
	}}
	srv := sseServer(t, h)
	resp, err := http.Get(srv.URL + "/ws/jobs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	waitSubscribers(t, hub, "jobs", 1)
	hub.Publish("jobs", Event{Type: "hidden", At: time.Now()})
	hub.Publish("jobs", Event{Type: "shown", At: time.Now()})
	frames := readFrames(t, resp.Body, 1)
	if !strings.Contains(frames[0], "shown") || strings.Contains(frames[0], "hidden") {
		t.Fatalf("frames = %q", frames)
	}
}
