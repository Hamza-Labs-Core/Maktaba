package subscriptions

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

type countStub int

func (c countStub) CountUsers(context.Context) (int, error) { return int(c), nil }

func signed(t *testing.T, priv ed25519.PrivateKey, inner subscriptions.LicenseInner) []byte {
	t.Helper()
	lic, err := subscriptions.Sign(priv, inner)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(lic)
	return b
}

func do(ctx context.Context, r http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// Every admin route refuses no one and a viewer (the API enforces the
// role; the web only hides).
func TestLicenseRoutes_AdminOnly(t *testing.T) {
	_, r, priv := newHandler(t)
	now := time.Now().UTC()
	body := signed(t, priv, subscriptions.LicenseInner{LicenseID: "x", Tier: subscriptions.TierHome, IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	routes := []struct {
		method, path string
		body         []byte
	}{
		{http.MethodGet, "/api/admin/license", nil},
		{http.MethodPost, "/api/admin/license", body},
		{http.MethodDelete, "/api/admin/license", nil},
		{http.MethodPost, "/api/admin/license/refresh", nil},
	}
	for _, rt := range routes {
		for name, ctx := range map[string]context.Context{"no one": context.Background(), "viewer": userCtx()} {
			if rec := do(ctx, r, rt.method, rt.path, rt.body); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s: %d, want 403", rt.method, rt.path, name, rec.Code)
			}
		}
		if rec := do(adminCtx(), r, rt.method, rt.path, rt.body); rec.Code >= 400 {
			t.Errorf("%s %s as admin: %d %s", rt.method, rt.path, rec.Code, rec.Body.String())
		}
	}
	if rec := do(context.Background(), r, http.MethodGet, "/api/entitlements", nil); rec.Code != http.StatusForbidden {
		t.Errorf("entitlements as no one: %d", rec.Code)
	}
}

// Story 16.4: the key is write-only — no answer carries the signature.
func TestLicense_WriteOnly(t *testing.T) {
	_, r, priv := newHandler(t)
	now := time.Now().UTC()
	body := signed(t, priv, subscriptions.LicenseInner{LicenseID: "lic-w", Tier: subscriptions.TierPro, IssuedAt: now, ExpiresAt: now.Add(time.Hour), Holder: "Owner"})
	var lic subscriptions.License
	_ = json.Unmarshal(body, &lic)
	for _, rec := range []*httptest.ResponseRecorder{
		do(adminCtx(), r, http.MethodPost, "/api/admin/license", body),
		do(adminCtx(), r, http.MethodGet, "/api/admin/license", nil),
		do(adminCtx(), r, http.MethodGet, "/api/entitlements", nil),
	} {
		if strings.Contains(rec.Body.String(), lic.Signature) || strings.Contains(rec.Body.String(), "signature") {
			t.Fatalf("an answer carried the license key: %s", rec.Body.String())
		}
	}
	var v LicenseView
	_ = json.Unmarshal(do(adminCtx(), r, http.MethodGet, "/api/admin/license", nil).Body.Bytes(), &v)
	if v.License == nil || v.License.Holder != "Owner" || v.Tier != subscriptions.TierPro || v.SeatsAllowed != subscriptions.SeatsUnlimited {
		t.Fatalf("view: %+v", v)
	}
}

// Refusals say why in plain words; nothing is applied.
func TestLicense_RefusalsArePlain(t *testing.T) {
	h, r, priv := newHandler(t)
	_, other, _ := ed25519.GenerateKey(nil)
	now := time.Now().UTC()
	h.Cloud = func() CloudLink { return CloudLink{Claimed: true, ServerID: "srv-1"} }
	tampered := signed(t, priv, subscriptions.LicenseInner{LicenseID: "t", Tier: subscriptions.TierHome, IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	tampered = bytes.Replace(tampered, []byte(`"home"`), []byte(`"pro"`), 1)
	cases := map[string]struct {
		body []byte
		want string
	}{
		"tampered":    {tampered, "signature does not match"},
		"foreign key": {signed(t, other, subscriptions.LicenseInner{LicenseID: "f", Tier: subscriptions.TierPro, IssuedAt: now, ExpiresAt: now.Add(time.Hour)}), "signature does not match"},
		"expired":     {signed(t, priv, subscriptions.LicenseInner{LicenseID: "e", Tier: subscriptions.TierPro, IssuedAt: now, ExpiresAt: now.Add(-time.Hour)}), "expired"},
		"other home":  {signed(t, priv, subscriptions.LicenseInner{LicenseID: "o", Tier: subscriptions.TierPro, IssuedAt: now, ExpiresAt: now.Add(time.Hour), ServerID: "srv-2"}), "another home"},
		"not json":    {[]byte("hello"), "not a Maktaba license"},
	}
	for name, c := range cases {
		rec := do(adminCtx(), r, http.MethodPost, "/api/admin/license", c.body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), c.want) || !strings.Contains(rec.Body.String(), TypeLicenseInvalid) {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if h.Store.Current() != nil {
		t.Fatal("a refused license was applied")
	}
}

// A build without the license key answers 503 with a plain reason.
func TestLicense_NoVerifier(t *testing.T) {
	h := &Handler{Store: subscriptions.NewStore()}
	r := chi.NewRouter()
	h.Mount(r)
	rec := do(adminCtx(), r, http.MethodPost, "/api/admin/license", []byte("{}"))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "cannot check licenses") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var v LicenseView
	_ = json.Unmarshal(do(adminCtx(), r, http.MethodGet, "/api/admin/license", nil).Body.Bytes(), &v)
	if v.Available || v.Tier != subscriptions.TierFree {
		t.Fatalf("view: %+v", v)
	}
}

// Story 16.4 EC: more users than seats — a warning; nothing locks.
func TestLicense_OverSeatWarning(t *testing.T) {
	h, r, _ := newHandler(t)
	h.SeatCount = countStub(2)
	var v LicenseView
	_ = json.Unmarshal(do(adminCtx(), r, http.MethodGet, "/api/admin/license", nil).Body.Bytes(), &v)
	if !v.OverSeats || v.SeatsUsed == nil || *v.SeatsUsed != 2 || v.SeatsAllowed != 1 || !strings.Contains(v.Message, "keep working") {
		t.Fatalf("over-seat view: %+v", v)
	}
}

// Applying writes an audit row with the actor; removing too.
func TestLicense_Audited(t *testing.T) {
	h, r, priv := newHandler(t)
	var events []string
	h.Audit = func(_ context.Context, actor, ev string, _ map[string]any) { events = append(events, actor+":"+ev) }
	now := time.Now().UTC()
	do(adminCtx(), r, http.MethodPost, "/api/admin/license", signed(t, priv, subscriptions.LicenseInner{LicenseID: "a", Tier: subscriptions.TierHome, IssuedAt: now, ExpiresAt: now.Add(time.Hour)}))
	do(adminCtx(), r, http.MethodDelete, "/api/admin/license", nil)
	if strings.Join(events, ",") != "admin:license.applied,admin:license.removed" {
		t.Fatalf("audit: %v", events)
	}
}
