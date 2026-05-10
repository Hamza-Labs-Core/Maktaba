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
	"os"
	"testing"
	"time"
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
