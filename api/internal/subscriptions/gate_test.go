package subscriptions

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func relayDo(ref *StoreRef, path string, via bool) *httptest.ResponseRecorder {
	h := RelayGate(ref)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if via {
		req.Header.Set(viaHeader, "relay")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Story 16.1: free is LAN-only — what the cloud relay forwards is
// refused with a plain reason; the LAN and health probes are not.
func TestRelayGate(t *testing.T) {
	ref := &StoreRef{}
	if rec := relayDo(ref, "/api/videos", true); rec.Code != http.StatusForbidden {
		t.Fatalf("free via relay: %d", rec.Code)
	} else {
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body["type"] != TypeLicenseRequired || body["feature"] != "cloud_relay" || body["required_tier"] != "home" ||
			!strings.Contains(body["detail"].(string), "This home is on Free") {
			t.Fatalf("refusal: %v", body)
		}
	}
	if rec := relayDo(ref, "/api/videos", false); rec.Code != http.StatusNoContent {
		t.Fatalf("free on the LAN: %d", rec.Code)
	}
	if rec := relayDo(ref, "/healthz", true); rec.Code != http.StatusNoContent {
		t.Fatalf("health via relay: %d", rec.Code)
	}
	s := NewStore()
	s.Set(&Entitlements{Tier: TierHome, Features: map[Feature]bool{FeatureCloudRelay: true}})
	ref.Set(s)
	if rec := relayDo(ref, "/api/videos", true); rec.Code != http.StatusNoContent {
		t.Fatalf("home via relay: %d", rec.Code)
	}
}

func TestRequireFeature(t *testing.T) {
	ref := &StoreRef{}
	h := RequireFeature(ref, FeatureFederation)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "needs the Pro plan") {
		t.Fatalf("free federation: %d %s", rec.Code, rec.Body.String())
	}
	s := NewStore()
	s.Set(&Entitlements{Tier: TierPro, Features: map[Feature]bool{FeatureFederation: true}})
	ref.Set(s)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	if rec.Code != 204 {
		t.Fatalf("pro federation: %d", rec.Code)
	}
}

func TestRequiredTier(t *testing.T) {
	for f, want := range map[Feature]Tier{FeatureCloudRelay: TierHome, FeatureMultiUser: TierHome, FeatureFederation: TierPro} {
		if got := RequiredTier(f); got != want {
			t.Errorf("%s: %s want %s", f, got, want)
		}
	}
}
