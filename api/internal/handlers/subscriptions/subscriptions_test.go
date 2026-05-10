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
	if rec.Code != http.StatusNoContent {
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
	if h.Store.Allows(subscriptions.FeatureCloudRelay) {
		t.Fatal("revoke did not revert to free tier")
	}
}
