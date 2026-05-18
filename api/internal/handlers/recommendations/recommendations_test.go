package recommendations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

func authReq(method, target string, uid string, admin bool) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	ctx := principal.WithPrincipal(req.Context(), &principal.Principal{UserID: uid, IsAdmin: admin})
	return req.WithContext(ctx)
}

func TestGet_RequiresPrincipal(t *testing.T) {
	h := &Handler{}
	r := chi.NewRouter()
	h.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/recommendations", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rec.Code)
	}
}

// With a nil DB every rail query errors and degrades to empty; the
// envelope must still carry the Story 14.7 fields and never 500.
func TestGet_EnvelopeShape_DegradesGracefully(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	h := &Handler{NowFunc: func() time.Time { return now }, CacheTTL: 30 * time.Second}
	r := chi.NewRouter()
	h.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, authReq(http.MethodGet, "/api/recommendations?surface=tv-home", "u1", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.GeneratedAt.Equal(now) {
		t.Fatalf("generated_at=%v want %v", resp.GeneratedAt, now)
	}
	if !resp.ExpiresAt.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("expires_at=%v", resp.ExpiresAt)
	}
	// No DB rows: rails empty (all degraded), but field present.
	if resp.Rails == nil {
		t.Fatal("rails must be non-nil array")
	}
}

func TestGet_LimitClampedToMaxItems(t *testing.T) {
	h := &Handler{}
	r := chi.NewRouter()
	h.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, authReq(http.MethodGet, "/api/recommendations?limit=9999", "u1", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	// Clamp is internal; assert it does not error and that maxItems is 20.
	if maxItems != 20 || maxRows != 5 {
		t.Fatalf("caps changed: items=%d rows=%d", maxItems, maxRows)
	}
}

func TestDismissRow_RequiresPrincipal(t *testing.T) {
	h := &Handler{}
	r := chi.NewRouter()
	h.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/recommendations/rows/for_you", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestRefresh_RequiresAdmin(t *testing.T) {
	h := &Handler{}
	r := chi.NewRouter()
	h.Mount(r)

	// Non-admin principal -> 403.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, authReq(http.MethodPost, "/api/recommendations/refresh", "u1", false))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin status=%d", rec.Code)
	}

	// Admin -> 202 and cache cleared (no DB needed, cache is in-memory).
	h.cacheStore("u1:tv-home", Response{})
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, authReq(http.MethodPost, "/api/recommendations/refresh", "admin", true))
	if rec2.Code != http.StatusAccepted {
		t.Fatalf("admin status=%d", rec2.Code)
	}
	h.cacheMu.Lock()
	n := len(h.cache)
	h.cacheMu.Unlock()
	if n != 0 {
		t.Fatalf("cache not cleared, len=%d", n)
	}
}

func TestFilterItems_DropsDismissed_PreservesOrder(t *testing.T) {
	in := []Item{{VideoID: "a"}, {VideoID: "b"}, {VideoID: "c"}}
	out := filterItems(in, map[string]bool{"b": true})
	if len(out) != 2 || out[0].VideoID != "a" || out[1].VideoID != "c" {
		t.Fatalf("got %+v", out)
	}
	// Empty dismissal set is a passthrough.
	if same := filterItems(in, nil); len(same) != 3 {
		t.Fatalf("passthrough changed len=%d", len(same))
	}
}

func TestBustUser_OnlyClearsThatUser(t *testing.T) {
	h := &Handler{}
	h.cacheStore("u1:tv-home", Response{})
	h.cacheStore("u1:web-home", Response{})
	h.cacheStore("u2:tv-home", Response{})
	h.bustUser("u1")
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	if _, ok := h.cache["u1:tv-home"]; ok {
		t.Fatal("u1 tv-home not busted")
	}
	if _, ok := h.cache["u1:web-home"]; ok {
		t.Fatal("u1 web-home not busted")
	}
	if _, ok := h.cache["u2:tv-home"]; !ok {
		t.Fatal("u2 wrongly busted")
	}
}

func TestRailReasonKindsAreStable(t *testing.T) {
	if ReasonContinueWatching != "continue_watching" ||
		ReasonForYou != "for_you" ||
		ReasonFromLibrary != "from_library" {
		t.Fatal("reason_kind constants changed — client localization contract")
	}
}
