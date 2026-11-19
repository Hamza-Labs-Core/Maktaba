package subscriptions

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

func newHandler(t *testing.T) (*Handler, http.Handler, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := &Handler{
		Store:    subscriptions.NewStore(),
		Verifier: &subscriptions.Verifier{PublicKey: pub},
	}
	r := chi.NewRouter()
	h.Mount(r)
	return h, r, priv
}

func adminCtx() context.Context {
	return principal.WithPrincipal(context.Background(),
		&principal.Principal{UserID: "admin", IsAdmin: true})
}

func userCtx() context.Context {
	return principal.WithPrincipal(context.Background(),
		&principal.Principal{UserID: "user"})
}

func TestGetEntitlementsReturnsFreeTierByDefault(t *testing.T) {
	_, r, _ := newHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/entitlements", nil).WithContext(userCtx())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var resp Response
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Tier != subscriptions.TierFree {
		t.Fatalf("tier=%s", resp.Tier)
	}
	if resp.Features[subscriptions.FeatureCloudRelay] {
		t.Fatal("cloud_relay should be off in free tier")
	}
}

func TestSetLicenseRequiresAdmin(t *testing.T) {
	_, r, _ := newHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/license",
		bytes.NewReader([]byte("{}"))).WithContext(userCtx())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestSetAndRevokeLicense(t *testing.T) {
	h, r, priv := newHandler(t)
	now := time.Now().UTC()
	lic, _ := subscriptions.Sign(priv, subscriptions.LicenseInner{
		LicenseID: "lic-1",
		Tier:      subscriptions.TierPremium,
		Seats:     5,
		IssuedAt:  now,
		ExpiresAt: now.Add(365 * 24 * time.Hour),
	})
	body, _ := json.Marshal(lic)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/license",
		bytes.NewReader(body)).WithContext(adminCtx())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !h.Store.Allows(subscriptions.FeatureCloudRelay) {
		t.Fatal("license did not unlock cloud_relay")
	}

	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2,
		httptest.NewRequest(http.MethodDelete, "/api/admin/license", nil).WithContext(adminCtx()))
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d", rec2.Code)
	}
	// story 16.2: a removed license's plan keeps working, fully, for its grace
	if st := h.Store.State(); st.License != nil || st.Status != subscriptions.StatusGrace ||
		!h.Store.Allows(subscriptions.FeatureCloudRelay) {
		t.Fatalf("after revoke: %+v", st)
	}
}

// Story 16.2: the admin sees how many days the grace has left, counted from the
// license's expiry, and what happens then.
func TestTheAdminSeesTheGraceDaysLeft(t *testing.T) {
	h, r, priv := newHandler(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	h.Store.SetClock(func() time.Time { return now })
	exp := now.Add(24 * time.Hour)
	lic, _ := subscriptions.Sign(priv, subscriptions.LicenseInner{LicenseID: "lic-g", Tier: subscriptions.TierPro,
		IssuedAt: now, ExpiresAt: exp})
	ent, err := h.Verifier.Verify(lic, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Store.SetLicense(context.Background(), lic, ent); err != nil {
		t.Fatal(err)
	}
	get := func() LicenseView {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/license", nil).WithContext(adminCtx()))
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d %s", rec.Code, rec.Body)
		}
		var v LicenseView
		_ = json.NewDecoder(rec.Body).Decode(&v)
		return v
	}
	if v := get(); v.GraceUntil != nil || v.GraceDaysLeft != nil || v.Status != subscriptions.StatusActive {
		t.Fatalf("an active license shows a grace: %+v", v)
	}
	now = exp.Add(3*24*time.Hour + time.Hour) // 26 days and 23 hours left: 27
	v := get()
	if v.Status != subscriptions.StatusGrace || v.GraceDaysLeft == nil || *v.GraceDaysLeft != 27 ||
		v.GraceUntil == nil || !v.GraceUntil.Equal(exp.Add(subscriptions.Grace)) {
		t.Fatalf("in the grace: %+v", v)
	}
	if !v.Features[subscriptions.FeatureFederation] || v.Tier != subscriptions.TierPro {
		t.Fatalf("the grace is the full plan: %+v", v)
	}
	if want := "keep working for 27 more days, until 2 December 2026"; !bytes.Contains([]byte(v.Message), []byte(want)) {
		t.Fatalf("message %q lacks %q", v.Message, want)
	}
	now = exp.Add(subscriptions.Grace)
	if v := get(); v.Status != subscriptions.StatusExpired || v.GraceDaysLeft != nil || v.Tier != subscriptions.TierFree {
		t.Fatalf("after the grace: %+v", v)
	}
}

// Story 16.2: a downgrade shows the previous plan's days left beside the active plan; a
// removed license says so.
func TestTheAdminSeesADowngradesGraceAndARemoval(t *testing.T) {
	h, r, priv := newHandler(t)
	now := time.Now().UTC()
	apply := func(id string, tier subscriptions.Tier) {
		lic, _ := subscriptions.Sign(priv, subscriptions.LicenseInner{LicenseID: id, Tier: tier, IssuedAt: now,
			ExpiresAt: now.Add(31 * 24 * time.Hour)})
		ent, err := h.Verifier.Verify(lic, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.Store.SetLicense(context.Background(), lic, ent); err != nil {
			t.Fatal(err)
		}
	}
	get := func() LicenseView {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/license", nil).WithContext(adminCtx()))
		var v LicenseView
		_ = json.NewDecoder(rec.Body).Decode(&v)
		return v
	}
	apply("pro-1", subscriptions.TierPro)
	apply("home-1", subscriptions.TierHome)
	v := get()
	if v.Status != subscriptions.StatusActive || v.GraceDaysLeft == nil || *v.GraceDaysLeft != 30 ||
		!bytes.Contains([]byte(v.Message), []byte("This home's plan changed. The previous plan's features keep working for 30 more days")) {
		t.Fatalf("a downgrade: %+v", v)
	}
	if err := h.Store.Revoke(context.Background()); err != nil {
		t.Fatal(err)
	}
	v = get()
	if v.Status != subscriptions.StatusGrace || v.License != nil ||
		!bytes.Contains([]byte(v.Message), []byte("This home's license was removed. Its paid features keep working for 30 more days")) {
		t.Fatalf("a removal: %+v", v)
	}
}
