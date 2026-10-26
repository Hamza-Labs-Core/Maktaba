package subscriptions

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCloud is the license server (revocation list) and the relay agent
// (the plan license) in one server.
type fakeCloud struct {
	mu        sync.Mutex
	priv      ed25519.PrivateKey
	revoked   []string
	planCode  int
	planBody  any
	date      time.Time
	revCalls  int
	planCalls int
}

func (f *fakeCloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.date.IsZero() {
		w.Header().Set("Date", f.date.UTC().Format(http.TimeFormat))
	}
	switch r.URL.Path {
	case "/v1/licenses/revocations":
		f.revCalls++
		list, _ := SignRevocations(f.priv, RevocationsInner{IssuedAt: time.Now().UTC(), LicenseIDs: f.revoked})
		_ = json.NewEncoder(w).Encode(list)
	case "/admin/cloud-link/license":
		f.planCalls++
		w.WriteHeader(f.planCode)
		_ = json.NewEncoder(w).Encode(f.planBody)
	default:
		http.NotFound(w, r)
	}
}

func newRefresher(t *testing.T, f *fakeCloud, s *Store, pub ed25519.PublicKey, linked bool) *Refresher {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	r := &Refresher{
		Store: s, Verifier: &Verifier{PublicKey: pub},
		ServerURL: func() string { return srv.URL },
	}
	if linked {
		r.AgentAddr = strings.TrimPrefix(srv.URL, "http://")
		r.ServerID = func() string { return "srv-1" }
	}
	return r
}

func planLicense(t *testing.T, priv ed25519.PrivateKey, id string, tier Tier, server string) map[string]any {
	t.Helper()
	now := time.Now().UTC()
	lic, err := Sign(priv, LicenseInner{LicenseID: id, Tier: tier, IssuedAt: now, ExpiresAt: now.Add(31 * 24 * time.Hour), ServerID: server})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"license": lic}
}

// Story 16.3/16.4: a paid plan becomes the home's license; a plan that
// ends sends the home back to free; a revoked plan locks at once.
func TestRefresher_PlanLicenseLifecycle(t *testing.T) {
	pub, priv := newKey(t)
	f := &fakeCloud{priv: priv, planCode: 200}
	f.planBody = planLicense(t, priv, "cloud-srv-1", TierHome, "srv-1")
	s := NewStore()
	r := newRefresher(t, f, s, pub, true)
	ctx := context.Background()

	if err := r.RefreshNow(ctx); err != nil {
		t.Fatal(err)
	}
	if s.TierName() != "home" || s.SeatLimit() != 4 {
		t.Fatalf("after the plan: tier %s seats %d", s.TierName(), s.SeatLimit())
	}
	if st := s.State(); st.LastRefreshAt.IsZero() || st.LastRefreshError != "" {
		t.Fatalf("refresh not recorded: %+v", st)
	}

	// an upgrade arrives on the next check
	f.mu.Lock()
	f.planBody = planLicense(t, priv, "cloud-srv-1", TierPro, "srv-1")
	f.mu.Unlock()
	if err := r.RefreshNow(ctx); err != nil {
		t.Fatal(err)
	}
	if s.TierName() != "pro" {
		t.Fatalf("upgrade not applied: %s", s.TierName())
	}

	// revoked (a dispute): locked at once
	f.mu.Lock()
	f.planCode, f.planBody = 403, map[string]string{"error": "license_revoked"}
	f.mu.Unlock()
	if err := r.RefreshNow(ctx); err != nil {
		t.Fatal(err)
	}
	if st := s.State(); st.Status != StatusRevoked || s.Current() != nil {
		t.Fatalf("revoked plan: %q", st.Status)
	}

	// the dispute is resolved: the same license comes back and unlocks
	f.mu.Lock()
	f.planCode, f.planBody = 200, planLicense(t, priv, "cloud-srv-1", TierPro, "srv-1")
	f.mu.Unlock()
	if err := r.RefreshNow(ctx); err != nil {
		t.Fatal(err)
	}
	if s.TierName() != "pro" {
		t.Fatalf("resolved dispute: tier %s status %q", s.TierName(), s.State().Status)
	}

	// the plan ends: free, the license gone
	f.mu.Lock()
	f.planCode, f.planBody = 404, map[string]string{"error": "no_license"}
	f.mu.Unlock()
	if err := r.RefreshNow(ctx); err != nil {
		t.Fatal(err)
	}
	if st := s.State(); st.License != nil || st.Status != StatusNone {
		t.Fatalf("plan ended: %+v", st)
	}
}

// A license for another home, or one not signed by the license key, is
// refused and the home keeps what it had.
func TestRefresher_RefusesForeignOrForgedPlanLicense(t *testing.T) {
	pub, priv := newKey(t)
	_, other := newKey(t)
	for name, body := range map[string]map[string]any{
		"other_home": planLicense(t, priv, "x", TierPro, "srv-2"),
		"forged":     planLicense(t, other, "x", TierPro, "srv-1"),
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeCloud{priv: priv, planCode: 200, planBody: body}
			s := NewStore()
			r := newRefresher(t, f, s, pub, true)
			if err := r.RefreshNow(context.Background()); err == nil {
				t.Fatal("expected a refresh error")
			}
			if s.Current() != nil {
				t.Fatal("a foreign or forged license was applied")
			}
			if s.State().LastRefreshError == "" {
				t.Fatal("the error must reach Settings")
			}
		})
	}
}

// A plan license does not replace a higher offline license applied by
// hand, and "no plan" does not remove it.
func TestRefresher_KeepsOfflineLicense(t *testing.T) {
	pub, priv := newKey(t)
	s := NewStore()
	applyLicense(t, s, &Verifier{PublicKey: pub}, priv, LicenseInner{LicenseID: "offline-pro", Tier: TierPro, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(365 * 24 * time.Hour)})
	f := &fakeCloud{priv: priv, planCode: 200, planBody: planLicense(t, priv, "cloud-srv-1", TierHome, "srv-1")}
	r := newRefresher(t, f, s, pub, true)
	if err := r.RefreshNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Current().LicenseID != "offline-pro" {
		t.Fatalf("plan license replaced a higher offline license: %s", s.Current().LicenseID)
	}
	f.mu.Lock()
	f.planCode, f.planBody = 404, map[string]string{"error": "no_license"}
	f.mu.Unlock()
	if err := r.RefreshNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Current() == nil {
		t.Fatal("the cloud's 'no plan' removed an offline license")
	}
}

// Story 16.4: an offline home keeps its license and the last list; the
// error is recorded for the admin; a revoked offline license locks from
// the list; the list is fetched once a day, not every check.
func TestRefresher_RevocationsDailyAndOffline(t *testing.T) {
	pub, priv := newKey(t)
	s := NewStore()
	applyLicense(t, s, &Verifier{PublicKey: pub}, priv, LicenseInner{LicenseID: "off-1", Tier: TierHome, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(365 * 24 * time.Hour)})
	f := &fakeCloud{priv: priv}
	r := newRefresher(t, f, s, pub, false)
	ctx := context.Background()
	if err := r.refresh(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := r.refresh(ctx, false); err != nil {
		t.Fatal(err)
	}
	if f.revCalls != 1 {
		t.Fatalf("revocation list fetched %d times in a day, want 1", f.revCalls)
	}

	// offline: the error is recorded, the license stands
	down := &Refresher{Store: s, Verifier: &Verifier{PublicKey: pub}, ServerURL: func() string { return "http://127.0.0.1:1" }}
	if err := down.RefreshNow(ctx); err == nil {
		t.Fatal("expected an unreachable error")
	}
	if s.Current() == nil || s.State().LastRefreshError == "" {
		t.Fatal("offline: license must stand and the error be recorded")
	}

	f.mu.Lock()
	f.revoked = []string{"off-1"}
	f.mu.Unlock()
	if err := r.RefreshNow(ctx); err != nil {
		t.Fatal(err)
	}
	if s.State().Status != StatusRevoked {
		t.Fatal("a listed offline license must lock")
	}
}

// Story 16.4 EC: the license server's clock wins over the local clock.
func TestRefresher_TrustsServerDate(t *testing.T) {
	pub, priv := newKey(t)
	local := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) // set back
	server := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	s := NewStore()
	s.SetClock(func() time.Time { return local })
	applyLicense(t, s, &Verifier{PublicKey: pub}, priv, LicenseInner{LicenseID: "l", Tier: TierHome, IssuedAt: local, ExpiresAt: local.Add(60 * 24 * time.Hour)})
	f := &fakeCloud{priv: priv, date: server}
	r := newRefresher(t, f, s, pub, false)
	if err := r.RefreshNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Current() != nil {
		t.Fatal("the license server's date shows the license expired; the local clock must not keep it")
	}
}
