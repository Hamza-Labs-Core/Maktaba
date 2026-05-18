package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/auth"
)

// principalForCookieTest returns a context carrying a cookie-sourced
// principal when the request presents the session cookie. Mirrors what
// the real CookieAuth middleware does, minus the DB lookup.
func principalForCookieTest(r *http.Request) context.Context {
	if c, err := r.Cookie(auth.CookieSession); err == nil && c.Value != "" {
		return principal.WithPrincipal(r.Context(), &principal.Principal{
			UserID: "u-cookie", Source: principal.SourceCookie,
		})
	}
	return r.Context()
}

// TestServeWithAuth_JWKSAndHeaders is the integration sanity check
// for Stories 10.6 (JWKS publication) and 10.15 (security headers /
// CORS) wiring at the serve loop. Runs the same dance as
// TestServeIntegration but provides JWT keys via env so the JWKS
// endpoint actually publishes a non-empty document.
func TestServeWithAuth_JWKSAndHeaders(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping under -short")
	}

	const publicAddr = "127.0.0.1:18081"
	const adminAddr = "127.0.0.1:19101"
	if !portFree(t, publicAddr) || !portFree(t, adminAddr) {
		t.Skipf("integration: %s or %s in use", publicAddr, adminAddr)
	}

	priv, pub := mustPEMPair(t)
	t.Setenv("MAKTABA_HTTP_ADDR", publicAddr)
	t.Setenv("MAKTABA_ADMIN_ADDR", adminAddr)
	t.Setenv("MAKTABA_HEALTH_WARM", "0s")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("MAKTABA_GRPC_PEERS", "")
	t.Setenv("MAKTABA_HEALTH_PEERS", "")
	t.Setenv("MAKTABA_ENV", "test")
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", priv)
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", pub)
	t.Setenv("MAKTABA_HSTS", "1")
	t.Setenv("MAKTABA_CORS_ALLOWED_ORIGINS", "https://app.maktaba.local")

	done := make(chan error, 1)
	go func() { done <- runServe() }()
	t.Cleanup(func() {
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Log("runServe did not exit within 5 s after SIGINT")
		}
	})

	if err := waitForPort(adminAddr, 2*time.Second); err != nil {
		t.Fatalf("admin port did not come up: %v", err)
	}

	// JWKS publication (Story 10.6 AC-3).
	resp := mustGet(t, "http://"+publicAddr+"/api/.well-known/jwks.json")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("jwks status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("jwks cache-control = %q", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var jwks struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(body, &jwks); err != nil {
		t.Fatalf("jwks json: %v", err)
	}
	if len(jwks.Keys) != 1 {
		t.Errorf("jwks should contain 1 key, got %d", len(jwks.Keys))
	}

	// Security headers (Story 10.15 AC-2, AC-5).
	resp2 := mustGet(t, "http://"+publicAddr+"/api/system/health")
	defer resp2.Body.Close()
	wantHeaders := map[string]string{
		"Strict-Transport-Security":  "max-age=31536000; includeSubDomains",
		"X-Content-Type-Options":     "nosniff",
		"Referrer-Policy":            "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy": "same-origin",
	}
	for k, want := range wantHeaders {
		if got := resp2.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if resp2.Header.Get("Content-Security-Policy") == "" {
		t.Error("CSP header should be present")
	}
}

// TestApplySecurity_GatesBusinessRoutes is the R3.2 regression: the
// previously-anonymous business surface must now require auth. We wrap
// a sentinel "business" handler with the real applySecurity stack and
// assert anonymous is 401 while a valid bearer reaches the handler.
func TestApplySecurity_GatesBusinessRoutes(t *testing.T) {
	priv, pub := mustPEMPair(t)
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", priv)
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", pub)
	t.Setenv("MAKTABA_ADMIN_TOKEN", "")
	t.Setenv("MAKTABA_CORS_ALLOWED_ORIGINS", "")
	t.Setenv("MAKTABA_HSTS", "")

	st, err := initAuth(noopLogger())
	if err != nil {
		t.Fatalf("initAuth: %v", err)
	}

	business := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("business-ok"))
	})
	// cookieAuth + csrf nil: the bearer path alone must still gate + admit.
	stack := st.applySecurity(business, nil, nil)

	// Anonymous business route → 401.
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/libraries", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous /api/libraries: status = %d, want 401", rec.Code)
	}

	// Public route → reaches handler anonymously.
	recPub := httptest.NewRecorder()
	stack.ServeHTTP(recPub, httptest.NewRequest(http.MethodGet, "/api/system/version", nil))
	if recPub.Code != http.StatusOK {
		t.Fatalf("anonymous /api/system/version: status = %d, want 200 (public)", recPub.Code)
	}

	// Valid bearer → reaches handler.
	tok := mustSignAPIToken(t, priv, pub)
	req := httptest.NewRequest(http.MethodGet, "/api/libraries", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	recAuth := httptest.NewRecorder()
	stack.ServeHTTP(recAuth, req)
	if recAuth.Code != http.StatusOK {
		t.Fatalf("bearer /api/libraries: status = %d, want 200", recAuth.Code)
	}
	if got := recAuth.Body.String(); got != "business-ok" {
		t.Fatalf("bearer body = %q, want business-ok", got)
	}
}

// mustSignAPIToken mints a minimal valid `aud:api` access token using
// the same key set initAuth loaded from the PEM env vars.
func mustSignAPIToken(t *testing.T, privPEM, pubPEM string) string {
	t.Helper()
	k, err := keys.FromPEM(privPEM, pubPEM)
	if err != nil {
		t.Fatalf("keys.FromPEM: %v", err)
	}
	set := keys.NewSet(time.Hour)
	set.Replace(k)
	tok, err := jwt.Sign(set, jwt.Claims{
		Iss: "maktaba", Aud: "api", Sub: "u1", Usr: "u1",
	})
	if err != nil {
		t.Fatalf("jwt.Sign: %v", err)
	}
	return tok
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// mustPEMPair generates a fresh RSA keypair (2048-bit, the minimum
// allowed by keys.Generate; 4096 would be slow for tests) and returns
// the PEM-encoded private and public halves.
func mustPEMPair(t *testing.T) (string, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	return privPEM, pubPEM
}

// TestApplySecurity_CSRFWiredForCookieSessions proves the CSRF
// double-submit guard is installed in the live applySecurity stack:
// a cookie-sourced principal making an unsafe request without the
// X-Maktaba-CSRF header is rejected 403, while a bearer client and a
// safe GET are unaffected. The cookie principal is supplied by a stub
// cookieAuth so this test stays DB-free.
func TestApplySecurity_CSRFWiredForCookieSessions(t *testing.T) {
	priv, pub := mustPEMPair(t)
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", priv)
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", pub)
	t.Setenv("MAKTABA_ADMIN_TOKEN", "")
	t.Setenv("MAKTABA_CORS_ALLOWED_ORIGINS", "")
	t.Setenv("MAKTABA_HSTS", "")

	st, err := initAuth(noopLogger())
	if err != nil {
		t.Fatalf("initAuth: %v", err)
	}

	business := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("business-ok"))
	})

	// stub cookieAuth: attach a cookie-sourced principal so the CSRF
	// guard's "only cookie sessions are CSRF-able" branch engages.
	cookieAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := principalForCookieTest(r)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	h := &auth.Handler{}
	stack := st.applySecurity(business, cookieAuth, h.CSRF)

	// Unsafe cookie request, no CSRF header → 403.
	req := httptest.NewRequest(http.MethodPost, "/api/libraries", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieSession, Value: "sess-1"})
	req.AddCookie(&http.Cookie{Name: auth.CookieCSRF, Value: "csrf-1"})
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cookie POST without CSRF header: status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}

	// Same request WITH matching CSRF header → 200.
	req2 := httptest.NewRequest(http.MethodPost, "/api/libraries", nil)
	req2.AddCookie(&http.Cookie{Name: auth.CookieSession, Value: "sess-1"})
	req2.AddCookie(&http.Cookie{Name: auth.CookieCSRF, Value: "csrf-1"})
	req2.Header.Set("X-Maktaba-CSRF", "csrf-1")
	rec2 := httptest.NewRecorder()
	stack.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("cookie POST with matching CSRF: status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}

	// Bearer client unaffected (no cookie, JWT source).
	tok := mustSignAPIToken(t, priv, pub)
	req3 := httptest.NewRequest(http.MethodPost, "/api/libraries", nil)
	req3.Header.Set("Authorization", "Bearer "+tok)
	rec3 := httptest.NewRecorder()
	stack.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("bearer POST: status = %d, want 200 (CSRF must not affect bearer)", rec3.Code)
	}
}

func TestInitAuth_RejectsMismatchedEnvPair(t *testing.T) {
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", "set")
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", "")
	t.Setenv("MAKTABA_ADMIN_TOKEN", "")
	if _, err := initAuth(noopLogger()); err == nil {
		t.Error("expected error when only one of the JWT PEM env vars is set")
	}
}

func TestInitAuth_RejectsShortAdminToken(t *testing.T) {
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", "")
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", "")
	t.Setenv("MAKTABA_ADMIN_TOKEN", "tooshort")
	if _, err := initAuth(noopLogger()); err == nil {
		t.Error("expected error for short admin token")
	}
}

func TestInitAuth_AcceptsLongAdminToken(t *testing.T) {
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", "")
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", "")
	t.Setenv("MAKTABA_ADMIN_TOKEN", fmt.Sprintf("%032d", 1))
	if _, err := initAuth(noopLogger()); err != nil {
		t.Errorf("32-char admin token should be accepted, got %v", err)
	}
}
