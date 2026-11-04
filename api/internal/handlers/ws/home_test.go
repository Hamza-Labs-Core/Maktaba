package ws

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// Story 51.09: /ws/home is open to any signed-in user (a viewer with no library here)
// and carries capabilities_changed with the ETag only; no sign-in opens no stream.
func TestSSE_HomeChannel(t *testing.T) {
	hub := NewHub()
	h := &Handler{Hub: hub}
	srv := sseServer(t, h)
	resp, err := http.Get(srv.URL + "/ws/home")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	waitSubscribers(t, hub, HomeChannel, 1)
	hub.Publish(HomeChannel, Event{Type: EventCapabilitiesChanged, At: time.Now(), Payload: map[string]any{"etag": "c-1843-1"}})
	frames := readFrames(t, resp.Body, 1)
	if !strings.Contains(frames[0], `"type":"capabilities_changed"`) || !strings.Contains(frames[0], `"etag":"c-1843-1"`) {
		t.Fatalf("frames %q", frames)
	}

	r := chi.NewRouter()
	h.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/ws/home", nil))
	if rec.Code < 400 {
		t.Fatalf("no sign-in: %d", rec.Code)
	}
}
