package flags

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	flagspkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/flags"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

const (
	adminID  = "00000000-0000-4000-8000-0000000000a1"
	viewerID = "00000000-0000-4000-8000-0000000000b2"
	someID   = "00000000-0000-4000-8000-0000000000c3"
)

func adminP() *principal.Principal {
	return &principal.Principal{UserID: adminID, IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceCookie}
}

func viewerP() *principal.Principal {
	return &principal.Principal{UserID: viewerID, Source: principal.SourceCookie}
}

// playP is an admin's Maktaba Cloud sign-in lowered to play scope.
func playP() *principal.Principal {
	p := adminP()
	p.ApplyCloudScope(principal.CloudScopePlay, nil)
	return p
}

// serve mounts h behind a stand-in for the credential chain: the
// request carries p (nil: no credential).
func serve(h *Handler) func(method, path string, body any, p *principal.Principal) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	h.Mount(r)
	return func(method, path string, body any, p *principal.Principal) *httptest.ResponseRecorder {
		var rd *bytes.Reader
		if body != nil {
			blob, _ := json.Marshal(body)
			rd = bytes.NewReader(blob)
		} else {
			rd = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rd)
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
}

var testNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

// fixedEnt is the license view: e (nil is the free tier). Swapping e is
// what applying or revoking a license does to the real store.
type fixedEnt struct{ e *subscriptions.Entitlements }

func (f *fixedEnt) Current() *subscriptions.Entitlements { return f.e }

type route struct{ method, path string }

var adminRoutes = []route{
	{http.MethodGet, "/api/admin/flags"},
	{http.MethodPost, "/api/admin/flags/overrides"},
	{http.MethodPatch, "/api/admin/flags/overrides/" + someID},
	{http.MethodDelete, "/api/admin/flags/overrides/" + someID},
	{http.MethodPost, "/api/admin/cohorts/early_access/users"},
	{http.MethodDelete, "/api/admin/cohorts/early_access/users/" + someID},
}

var meRoutes = []route{
	{http.MethodGet, "/api/me/flags"},
	{http.MethodGet, "/api/me/flags/keys"},
	{http.MethodGet, "/api/me/cohorts"},
	{http.MethodPost, "/api/me/cohorts"},
	{http.MethodDelete, "/api/me/cohorts/early_access"},
}

// The role rules hold before any database is read: a handler with none
// still refuses who it must (the admin passes the gate and meets 503).
func TestAdminRoutesRefuseNonAdmins(t *testing.T) {
	do := serve(New(nil, &fixedEnt{}, nil, nil))
	for _, rt := range adminRoutes {
		if rec := do(rt.method, rt.path, map[string]any{}, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a credential: %d, want 401", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, map[string]any{}, viewerP()); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a viewer: %d, want 403", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, map[string]any{}, playP()); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a play-scoped cloud sign-in: %d, want 403", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, map[string]any{}, adminP()); rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Errorf("%s %s as an admin: %d, want past the role gate", rt.method, rt.path, rec.Code)
		}
	}
}

func TestMeRoutesNeedACredential(t *testing.T) {
	do := serve(New(nil, &fixedEnt{}, nil, nil))
	for _, rt := range meRoutes {
		if rec := do(rt.method, rt.path, map[string]any{"cohort": "early_access"}, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a credential: %d, want 401", rt.method, rt.path, rec.Code)
		}
		for _, p := range []*principal.Principal{viewerP(), playP(), adminP()} {
			if rec := do(rt.method, rt.path, map[string]any{"cohort": "early_access"}, p); rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
				t.Errorf("%s %s signed in: %d, want past the role gate", rt.method, rt.path, rec.Code)
			}
		}
	}
}

func TestViews_MarksStaleUnknownExpired(t *testing.T) {
	rows := []flagspkg.Override{
		{ID: "1", FlagKey: flagspkg.FlagEarlyAccess, Scope: "user", ScopeValue: viewerID, Value: true},
		{ID: "2", FlagKey: flagspkg.FlagEarlyAccess, Scope: "user", ScopeValue: viewerID, Value: false},
		{ID: "3", FlagKey: "made.up.key", Scope: "global", Value: true},
	}
	v := views(rows, testNow)
	if !v[0].Stale || v[1].Stale {
		t.Errorf("the older of two same-scope rows is stale: %+v", v[:2])
	}
	if !v[2].Unknown || v[0].Unknown {
		t.Errorf("an undeclared key is unknown: %+v", v[2])
	}
	past := testNow.Add(-1)
	if got := views([]flagspkg.Override{{FlagKey: "x", Scope: "global", ExpiresAt: &past}}, testNow); !got[0].Expired {
		t.Error("a row past expires_at is expired")
	}
}
