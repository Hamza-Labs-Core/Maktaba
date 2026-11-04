package subscriptions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

func problemBody(t *testing.T, e *httperror.Error) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	httperror.Write(rec, httptest.NewRequest(http.MethodGet, "/api/videos", nil), e)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// Story 51.19 T09: RelayGate asks the cloud.relay component's Licensed condition too.
// While the component waits (its licence lapsed) a relayed request gets today's 403
// license-required, body unchanged; a condition that is not known (kill switch, nothing
// published) leaves the store's answer; a condition that says yes never opens a home
// whose store says no.
func TestRelayGateAsksTheComponent(t *testing.T) {
	home := NewStore()
	home.Set(&Entitlements{Tier: TierHome, Features: map[Feature]bool{FeatureCloudRelay: true}})
	ref := &StoreRef{}
	ref.Set(home)
	licensed, known := false, true
	ref.SetRelayLicensed(func() (bool, bool) { return licensed, known })

	rec := relayDo(ref, "/api/videos", true)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("waiting via relay: %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if want := problemBody(t, Refusal(FeatureCloudRelay, "home")); !reflect.DeepEqual(body, want) {
		t.Fatalf("body %v\nwant %v", body, want)
	}
	if _, ok := body["code"]; ok {
		t.Fatal("the relay's refusal gained a code")
	}
	if rec := relayDo(ref, "/api/videos", false); rec.Code != http.StatusNoContent {
		t.Fatalf("waiting on the LAN: %d", rec.Code)
	}
	if rec := relayDo(ref, "/healthz", true); rec.Code != http.StatusNoContent {
		t.Fatalf("health via relay while waiting: %d", rec.Code)
	}

	known = false
	if rec := relayDo(ref, "/api/videos", true); rec.Code != http.StatusNoContent {
		t.Fatalf("not known: %d", rec.Code)
	}
	licensed, known = true, true
	if rec := relayDo(ref, "/api/videos", true); rec.Code != http.StatusNoContent {
		t.Fatalf("licensed: %d", rec.Code)
	}
	ref.Set(NewStore()) // free: the store's own check still refuses
	if rec := relayDo(ref, "/api/videos", true); rec.Code != http.StatusForbidden {
		t.Fatalf("free with a condition that says yes: %d", rec.Code)
	}
}

// The component refusal is the licence's problem with code licence_missing and the
// component added; the rest of its body is the same.
func TestComponentRefusal(t *testing.T) {
	got := problemBody(t, ComponentRefusal("cloud.relay", FeatureCloudRelay, "free"))
	want := problemBody(t, Refusal(FeatureCloudRelay, "free"))
	want["code"], want["component"] = "licence_missing", "cloud.relay"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

// OnChange hears every change of the license or its standing.
func TestStoreOnChange(t *testing.T) {
	s := NewStore()
	n := 0
	s.OnChange(func() { n++ })
	ctx := context.Background()
	s.Set(&Entitlements{Tier: TierHome})
	if err := s.Lock(ctx, LockRevoked); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRevocations(ctx, &RevocationList{}, "", s.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("heard %d changes, want 4", n)
	}
	if err := s.RecordRefresh(ctx, nil); err != nil || n != 4 {
		t.Fatalf("a refresh with no new license is not a change: %d %v", n, err)
	}
}
