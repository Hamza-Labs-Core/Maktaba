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
)

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
	// cookieAuth nil: the bearer path alone must still gate + admit.
	stack := st.applySecurity(business, nil)

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

// TestApplySecurity_HSTSDefaultsOnWithoutEnv is the Story 23.3 AC-2
// regression: HSTS must be sent EVEN WHEN MAKTABA_HSTS is unset
// (secure-by-default). The pre-fix behaviour was opt-in (header
// absent unless MAKTABA_HSTS=1).
func TestApplySecurity_HSTSDefaultsOnWithoutEnv(t *testing.T) {
	priv, pub := mustPEMPair(t)
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", priv)
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", pub)
	t.Setenv("MAKTABA_ADMIN_TOKEN", "")
	t.Setenv("MAKTABA_CORS_ALLOWED_ORIGINS", "")
	os.Unsetenv("MAKTABA_HSTS") // explicitly unset: must still be on

	st, err := initAuth(noopLogger())
	if err != nil {
		t.Fatalf("initAuth: %v", err)
	}
	stack := st.applySecurity(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }),
		nil,
	)
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/system/version", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
		t.Fatalf("HSTS = %q, want default-on max-age=31536000; includeSubDomains", got)
	}
}

// TestApplySecurity_HSTSExplicitOptOut: an operator on a `.local`
// HTTP-only install clears HSTS with MAKTABA_HSTS=0.
func TestApplySecurity_HSTSExplicitOptOut(t *testing.T) {
	priv, pub := mustPEMPair(t)
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", priv)
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", pub)
	t.Setenv("MAKTABA_ADMIN_TOKEN", "")
	t.Setenv("MAKTABA_CORS_ALLOWED_ORIGINS", "")
	t.Setenv("MAKTABA_HSTS", "0")

	st, err := initAuth(noopLogger())
	if err != nil {
		t.Fatalf("initAuth: %v", err)
	}
	stack := st.applySecurity(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }),
		nil,
	)
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/system/version", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("HSTS = %q, want empty (explicit opt-out)", got)
	}
}

// TestApplySecurity_AuthRouteRateLimitWired is the Story 23.6 AC-1
// regression: the per-route table is live on the real applySecurity
// chain — /api/auth/login is capped at 10/min/IP, far below the
// generic limiter, and the cap fires before any credential work.
func TestApplySecurity_AuthRouteRateLimitWired(t *testing.T) {
	priv, pub := mustPEMPair(t)
	t.Setenv("MAKTABA_JWT_PRIVATE_KEY_PEM", priv)
	t.Setenv("MAKTABA_JWT_PUBLIC_KEY_PEM", pub)
	t.Setenv("MAKTABA_ADMIN_TOKEN", "")
	t.Setenv("MAKTABA_CORS_ALLOWED_ORIGINS", "")

	st, err := initAuth(noopLogger())
	if err != nil {
		t.Fatalf("initAuth: %v", err)
	}
	// Sentinel handler stands in for the login handler (out of lane).
	stack := st.applySecurity(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		nil,
	)

	var got429 bool
	for i := 0; i < 11; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		req.RemoteAddr = "203.0.113.42:6000"
		rec := httptest.NewRecorder()
		stack.ServeHTTP(rec, req)
		if i < 10 && rec.Code != http.StatusOK {
			t.Fatalf("login %d via chain: status=%d want 200", i+1, rec.Code)
		}
		if rec.Code == http.StatusTooManyRequests {
			got429 = true
			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("chain 429 must carry Retry-After")
			}
		}
	}
	if !got429 {
		t.Fatal("11th /api/auth/login through applySecurity should be 429 (per-route cap not wired)")
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
