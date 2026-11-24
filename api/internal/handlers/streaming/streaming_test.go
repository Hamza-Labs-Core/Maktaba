package streaming

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionDebouncer(t *testing.T) {
	d := newSessionDebouncer(time.Second)
	t0 := time.Now()
	if !d.Allow("s1", t0) {
		t.Fatal("first allow should pass")
	}
	if d.Allow("s1", t0.Add(500*time.Millisecond)) {
		t.Fatal("500ms after must be debounced")
	}
	if !d.Allow("s1", t0.Add(1100*time.Millisecond)) {
		t.Fatal("1.1s after must allow")
	}
	if !d.Allow("s2", t0) {
		t.Fatal("different session is independent")
	}
}

func TestCapCache_RefreshAfterTTL(t *testing.T) {
	c := &capCache{ttl: 60 * time.Second}
	count := 0
	fetch := func(context.Context) (Capabilities, error) {
		count++
		return Capabilities{Codecs: []string{"h264"}}, nil
	}
	now := time.Now()
	clock := func() time.Time { return now }
	v, fresh, err := c.GetOrFetch(context.Background(), fetch, clock)
	if err != nil || !fresh || count != 1 || v.Codecs[0] != "h264" {
		t.Fatalf("initial fetch: %v %v %v", err, fresh, count)
	}
	_, fresh, _ = c.GetOrFetch(context.Background(), fetch, clock)
	if fresh || count != 1 {
		t.Fatalf("expected cached read, got fresh=%v count=%d", fresh, count)
	}
	now = now.Add(61 * time.Second)
	_, fresh, _ = c.GetOrFetch(context.Background(), fetch, clock)
	if !fresh || count != 2 {
		t.Fatalf("expected refresh after 61s, got fresh=%v count=%d", fresh, count)
	}
}

// capsService answers Capabilities only (the rest of SessionService is not called here).
type capsService struct {
	SessionService
	caps Capabilities
}

func (s capsService) Capabilities(context.Context) (Capabilities, error) { return s.caps, nil }

// Story 13.18: GET /api/stream/capabilities lists the client profiles the home knows, which
// the desktop Player reads before it asks for desktop-mpv.
func TestGetCapabilities_ListsTheClientProfiles(t *testing.T) {
	h := &Handler{
		Service:  capsService{caps: Capabilities{Codecs: []string{"h264"}, ClientProfiles: []string{"desktop-mpv", "desktop-player"}}},
		CapCache: &capCache{ttl: time.Minute},
	}
	w := httptest.NewRecorder()
	h.GetCapabilities(w, httptest.NewRequest(http.MethodGet, "/api/stream/capabilities", nil))
	var got struct {
		ClientProfiles []string `json:"client_profiles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %s: %v", w.Code, w.Body.String(), err)
	}
	if len(got.ClientProfiles) != 2 || got.ClientProfiles[0] != "desktop-mpv" {
		t.Fatalf("client_profiles %v", got.ClientProfiles)
	}
}
