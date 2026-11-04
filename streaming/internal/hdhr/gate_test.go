package hdhr

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
)

// Epic 51: with livetv.hdhomerun off, discover.json and lineup.json answer 409
// component_off; on, they answer as before.
func TestHandler_GateOnDiscoverAndLineup(t *testing.T) {
	on := false
	h := New(&fakeRepo{dev: enabledDevice(), channels: sampleChannels()}, &fakeStreamer{})
	h.Gate = httpx.RequireComponent(func(string) bool { return on }, "livetv.hdhomerun")
	srv := mux(h)
	get := func(path string) int {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code
	}
	for _, p := range []string{"/discover.json", "/lineup.json"} {
		if c := get(p); c != http.StatusConflict {
			t.Errorf("%s off: %d, want 409", p, c)
		}
	}
	on = true
	for _, p := range []string{"/discover.json", "/lineup.json"} {
		if c := get(p); c != http.StatusOK {
			t.Errorf("%s on: %d, want 200", p, c)
		}
	}
}
