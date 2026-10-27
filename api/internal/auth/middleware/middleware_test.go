package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secret"
)

func setWithKey(t *testing.T) *keys.Set {
	t.Helper()
	s := keys.NewSet(time.Hour)
	k, err := keys.Generate(keys.MinBits)
	if err != nil {
		t.Fatal(err)
	}
	s.Replace(k)
	return s
}

func capture(p **principal.Principal) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*p = principal.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func TestAdminToken_BypassWithBearer(t *testing.T) {
	tok := secret.New("a-very-long-admin-token-1234567890")
	mw := AdminToken(tok)
	var got *principal.Principal
	h := mw(capture(&got))

	req := httptest.NewRequest("GET", "/api/x", nil)
	req.Header.Set("Authorization", "Bearer a-very-long-admin-token-1234567890")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got == nil {
		t.Fatal("admin-token should have attached a principal")
	}
	if got.UserID != users.SentinelAdminID {
		t.Errorf("UserID = %q, want sentinel %q", got.UserID, users.SentinelAdminID)
	}
	if !got.IsAdmin || !got.AccessAllLibraries {
		t.Errorf("admin token principal should be admin with all libraries: %+v", got)
	}
	if got.Source != principal.SourceAdminToken {
		t.Errorf("Source = %q, want admin_token", got.Source)
	}
}

func TestAdminToken_BypassWithCookie(t *testing.T) {
	tok := secret.New("a-very-long-admin-token-1234567890")
	mw := AdminToken(tok)
	var got *principal.Principal
	h := mw(capture(&got))

	req := httptest.NewRequest("GET", "/api/x", nil)
	req.AddCookie(&http.Cookie{Name: AdminCookieName, Value: "a-very-long-admin-token-1234567890"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got == nil {
		t.Fatal("cookie path should also bypass")
	}
}

func TestAdminToken_OneByteOff_Rejected(t *testing.T) {
	tok := secret.New("a-very-long-admin-token-1234567890")
	mw := AdminToken(tok)
	var got *principal.Principal
	h := mw(capture(&got))

	req := httptest.NewRequest("GET", "/api/x", nil)
	req.Header.Set("Authorization", "Bearer a-very-long-admin-token-1234567891") // last char different
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got != nil {
		t.Errorf("one-byte-off should not bypass; got principal %+v", got)
	}
}

func TestAdminToken_DisabledByEmptyConfig(t *testing.T) {
	mw := AdminToken(secret.Value{})
	var got *principal.Principal
	h := mw(capture(&got))
	req := httptest.NewRequest("GET", "/api/x", nil)
	// Even an empty Authorization shouldn't accidentally match the
	// empty configured token (presence check guards against this).
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got != nil {
		t.Errorf("empty config should never match; got principal %+v", got)
	}
}

func TestJWTBearer_AttachesPrincipal(t *testing.T) {
	s := setWithKey(t)
	tok, err := jwt.Sign(s, jwt.Claims{
		Iss: "maktaba", Aud: "api", Sub: "user-1", Usr: "user-1",
		Lib: []string{"lib-a"}, IsAdmin: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	mw := JWTBearer(s, "api")
	var got *principal.Principal
	h := mw(capture(&got))

	req := httptest.NewRequest("GET", "/api/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got == nil {
		t.Fatal("bearer JWT should have attached a principal")
	}
	if got.UserID != "user-1" || got.IsAdmin {
		t.Errorf("principal = %+v", got)
	}
	if !got.HasLibrary("lib-a") || got.HasLibrary("lib-b") {
		t.Errorf("Libraries = %v", got.Libraries)
	}
	if got.Source != principal.SourceJWT {
		t.Errorf("Source = %q, want jwt", got.Source)
	}
}

func TestJWTBearer_RejectsBadAudience(t *testing.T) {
	s := setWithKey(t)
	tok, err := jwt.Sign(s, jwt.Claims{Iss: "maktaba", Aud: "streaming", Sub: "x", Usr: "x"})
	if err != nil {
		t.Fatal(err)
	}
	mw := JWTBearer(s, "api")
	var got *principal.Principal
	h := mw(capture(&got))

	req := httptest.NewRequest("GET", "/api/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got != nil {
		t.Errorf("wrong-audience JWT should not attach a principal; got %+v", got)
	}
}

func TestRequireAuth_401WhenAnonymous(t *testing.T) {
	h := RequireAuth(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("inner handler should not run when anonymous")
	}))
	req := httptest.NewRequest("GET", "/api/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: status = %d, want 401", rec.Code)
	}
}

func TestRequireAuthExcept_GatesBusinessRoutesAnonymous(t *testing.T) {
	gate := RequireAuthExcept(DefaultPublicAllowlist())
	h := gate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/libraries", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous /api/libraries: status = %d, want 401", rec.Code)
	}
}

func TestRequireAuthExcept_AllowsAuthenticated(t *testing.T) {
	gate := RequireAuthExcept(DefaultPublicAllowlist())
	called := false
	h := gate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/libraries", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(),
		&principal.Principal{UserID: "u1"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("authed /api/libraries: status = %d called=%v, want 200/true", rec.Code, called)
	}
}

func TestRequireAuthExcept_PublicRoutesBypassGate(t *testing.T) {
	gate := RequireAuthExcept(DefaultPublicAllowlist())
	for _, path := range []string{
		"/healthz",
		"/api/.well-known/jwks.json",
		"/.well-known/security.txt",
		"/api/system/health",
		"/api/system/version",
		"/api/auth/login",
		"/api/auth/refresh",
	} {
		called := false
		h := gate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest("POST", path, nil) // anonymous, any verb
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !called {
			t.Errorf("public %s: status=%d called=%v, want 200/true (must bypass gate)",
				path, rec.Code, called)
		}
	}
}

func TestRequireAuthExcept_AllowlistIsExactNotPrefix(t *testing.T) {
	// `/api/auth/logout` must NOT inherit `/api/auth/login`'s public
	// status — the allowlist matches exact paths only.
	gate := RequireAuthExcept(DefaultPublicAllowlist())
	h := gate(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("logout should be gated, not public")
	}))
	req := httptest.NewRequest("POST", "/api/auth/logout", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/api/auth/logout anonymous: status = %d, want 401", rec.Code)
	}
}

func TestRequireAdmin_403ForNonAdmin(t *testing.T) {
	h := RequireAdmin(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("inner handler should not run for non-admin")
	}))
	req := httptest.NewRequest("GET", "/api/x", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{
		UserID: "u", IsAdmin: false,
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: status = %d, want 403", rec.Code)
	}
}

func TestRequireAdmin_AllowsAdmin(t *testing.T) {
	called := false
	h := RequireAdmin(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	req := httptest.NewRequest("GET", "/api/x", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{
		UserID: "u", IsAdmin: true,
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("admin: status = %d, want 200", rec.Code)
	}
	if !called {
		t.Error("inner handler should have run")
	}
}

func TestIsPublicChannelArt(t *testing.T) {
	public := []string{
		"/api/channels/c1/logo",
		"/api/channels/c1/art/logo",
		"/api/channels/c1/art/watermark",
		"/api/channels/c1/art/loading",
	}
	for _, p := range public {
		if !IsPublicChannelArt(httptest.NewRequest("GET", p, nil)) {
			t.Errorf("GET %s should be public", p)
		}
		if IsPublicChannelArt(httptest.NewRequest("POST", p, nil)) {
			t.Errorf("POST %s must not be public", p)
		}
	}
	for _, p := range []string{
		"/api/channels/c1", "/api/channels", "/api/channels//logo", "/api/channels/c1/art/other",
		"/api/channels/c1/art/logo/x", "/api/channels/c1/guide", "/api/videos/c1/logo",
	} {
		if IsPublicChannelArt(httptest.NewRequest("GET", p, nil)) {
			t.Errorf("GET %s must not be public", p)
		}
	}
	gate := RequireAuthExcept(DefaultPublicAllowlist())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, httptest.NewRequest("GET", "/api/channels/c1/art/watermark", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous art GET: %d", rec.Code)
	}
}

// The release mirror's routes pass the gate for a request the home verified as a
// machine's (the delegation middleware runs first), and for nothing else: no user, no
// machine, no pass; a machine's signature opens no other route.
func TestMachineRoutesPassForAMachineOnly(t *testing.T) {
	gate := RequireAuthExcept(DefaultPublicAllowlist())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	get := func(path string, machine bool) int {
		req := httptest.NewRequest("GET", path, nil)
		if machine {
			req = req.WithContext(delegation.WithNode(req.Context(), "6f9c1d2e-1111-4222-8333-944455556666"))
		}
		rec := httptest.NewRecorder()
		gate.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, p := range []string{"/api/cluster/updates/artifacts/1.5.0/release.json",
		"/api/cluster/updates/wheelhouse/1.5.0/linux-amd64", "/api/cluster/updates/tuf/targets/ab.trusted_root.json"} {
		if c := get(p, true); c != http.StatusOK {
			t.Errorf("a machine's %s: %d", p, c)
		}
		if c := get(p, false); c != http.StatusUnauthorized {
			t.Errorf("an anonymous %s: %d", p, c)
		}
	}
	if c := get("/api/cluster/updates", true); c != http.StatusUnauthorized {
		t.Errorf("a machine's signature opened the updates page: %d", c)
	}
	// a worker-only machine converting: its download token (story 35.8 AC8) reaches the
	// artifact route alone (its handler verifies it), never the others
	withToken := func(path, header string) int {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", header)
		rec := httptest.NewRecorder()
		gate.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := withToken("/api/cluster/updates/artifacts/1.5.0/release.json", "Bearer mkt_dl_x.y"); c != http.StatusOK {
		t.Errorf("a download token on the artifact route: %d", c)
	}
	for _, p := range []string{"/api/cluster/updates/wheelhouse/1.5.0/linux-amd64", "/api/cluster/updates/tuf/root.json",
		"/api/cluster/updates"} {
		if c := withToken(p, "Bearer mkt_dl_x.y"); c != http.StatusUnauthorized {
			t.Errorf("a download token opened %s: %d", p, c)
		}
	}
	if c := withToken("/api/cluster/updates/artifacts/1.5.0/release.json", "Bearer mkt_pat_x"); c != http.StatusUnauthorized {
		t.Errorf("another bearer on the artifact route: %d", c)
	}
}

// The public allowlist is pinned: a route reaches a handler without a signed-in user only
// if it is on this list, and adding one is a change made here on purpose (the API enforces
// roles: a route left off the gate by mistake would be open to anyone). The admin's cluster
// routes, and every other admin route, stay behind the gate.
func TestDefaultPublicAllowlistIsPinned(t *testing.T) {
	want := []string{
		"/healthz", "/api/system/health", "/api/system/version", "/api/.well-known/jwks.json",
		"/.well-known/security.txt", "/api/auth/login", "/api/auth/refresh", "/api/auth/register",
		"/api/auth/cloud", // Epic 42 story 42.7: the cloud assertion is the credential
		"/api/pairing/device", "/api/pairing/device/token",
		"/api/cluster/join", "/api/cluster/join/*", "/api/cluster/agent",
		"/api/cluster/link", "/api/cluster/link/stream", "/api/cluster/ca.crt", "/api/cluster/link/pull",
		"/api/cluster/link/local/*", "/api/tv/remote/webhook", "/api/cluster/self", "/api/cluster/moved/*",
		"/api/cluster/kept-aside/report", "/api/cluster/term-claim",
		"/api/cluster/sso/authorize", "/api/cluster/sso/start", "/api/cluster/sso/callback",
		"/api/cluster/sso/exchange", "/api/cluster/delegate/introspect", "/api/cluster/delegate/signout",
		"/internal/updates/restored",
	}
	got := DefaultPublicAllowlist()
	if len(got) != len(want) {
		t.Errorf("the public allowlist has %d routes, the pinned list %d: update both on purpose", len(got), len(want))
	}
	for _, p := range want {
		if _, ok := got[p]; !ok {
			t.Errorf("pinned public route %s is not in the allowlist", p)
		}
		delete(got, p)
	}
	for p := range got {
		t.Errorf("route %s is public but not pinned here", p)
	}

	gate := RequireAuthExcept(DefaultPublicAllowlist())
	h := gate(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("%s reached its handler without a signed-in user", r.URL.Path)
	}))
	for _, path := range []string{
		"/api/cluster", "/api/cluster/settings", "/api/cluster/nodes/x", "/api/cluster/nodes/x/approve",
		"/api/cluster/nodes/x/revoke", "/api/users", "/api/settings", "/api/queues", "/api/admin/system/status",
		"/api/telemetry/app", "/api/telemetry/server/forget",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous %s: %d, want 401", path, rec.Code)
		}
	}
}

// A cloud sign-in's play token is never an admin, even one that says
// is_admin (the home mints play tokens without it; this is the second
// wall), and keeps what it may read. manage keeps the role.
func TestJWTBearer_CloudScopeCaps(t *testing.T) {
	s := setWithKey(t)
	for _, c := range []struct {
		scp       string
		wantAdmin bool
		wantCloud string
	}{
		{"play", false, principal.CloudScopePlay},
		{"bogus", false, principal.CloudScopePlay},
		{"manage", true, principal.CloudScopeManage},
		{"", true, ""},
	} {
		// a play cap ends all-library access ("sees everything"): a play
		// token reads only the libraries it names
		wantAll := c.wantAdmin
		tok, err := jwt.Sign(s, jwt.Claims{Iss: "maktaba", Aud: "api", Sub: "u", Usr: "u", IsAdmin: true, Scp: c.scp})
		if err != nil {
			t.Fatal(err)
		}
		var got *principal.Principal
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		JWTBearer(s, "api")(capture(&got)).ServeHTTP(httptest.NewRecorder(), req)
		if got == nil || got.IsAdmin != c.wantAdmin || got.CloudScope != c.wantCloud || got.AccessAllLibraries != wantAll {
			t.Errorf("scp %q: principal %+v", c.scp, got)
		}
		if got.ViaCloud() != (c.wantCloud != "") {
			t.Errorf("scp %q: ViaCloud %v", c.scp, got.ViaCloud())
		}
	}
}
