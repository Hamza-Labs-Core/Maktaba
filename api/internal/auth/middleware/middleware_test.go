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
