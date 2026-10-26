package cf

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// The public site's Worker (web/packages/tvsite) signs viewer tokens in
// TypeScript. RS256 (PKCS#1 v1.5) is deterministic, so for the same key and
// claims its token must equal SignToken's byte for byte: this fixture holds a
// test-only key, the claims, and the token SignToken makes. The Worker's
// tests sign the same claims and compare (sign.test.ts). Regenerate with
// MAKTABA_UPDATE_PARITY=1.

type parityFixture struct {
	Note  string `json:"_note"`
	PEM   string `json:"pem"`
	JWK   string `json:"jwk"`
	Kid   string `json:"kid"`
	Sub   string `json:"sub"`
	Now   int64  `json:"now"`
	TTL   int64  `json:"ttl_seconds"`
	Token string `json:"token"`
}

func parityPath() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "web", "packages", "tvsite", "src", "testdata", "parity.json")
}

func TestWorkerTokenParity(t *testing.T) {
	var fx parityFixture
	if os.Getenv("MAKTABA_UPDATE_PARITY") == "1" {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		fx = parityFixture{Note: "TEST-ONLY key, never used anywhere else; see api/internal/tvremote/cf/parity_test.go",
			PEM:  base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})),
			Kid:  "parity-kid-1", Sub: "ea95132c15732412d22c1476fa83f27a", Now: 1_793_000_000, TTL: 600}
	} else {
		b, err := os.ReadFile(parityPath())
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &fx); err != nil {
			t.Fatal(err)
		}
	}
	jwk, err := PrivateJWK(fx.PEM, fx.Kid)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := SignToken(SigningKey{ID: fx.Kid, PEM: fx.PEM}, fx.Sub, time.Duration(fx.TTL)*time.Second, time.Unix(fx.Now, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("MAKTABA_UPDATE_PARITY") == "1" {
		fx.JWK, fx.Token = jwk, tok
		b, _ := json.MarshalIndent(fx, "", "  ")
		if err := os.WriteFile(parityPath(), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if jwk != fx.JWK {
		t.Error("PrivateJWK changed: regenerate the fixture (MAKTABA_UPDATE_PARITY=1)")
	}
	if tok != fx.Token {
		t.Error("SignToken changed: the Worker's tokens no longer match the app's (regenerate and update sign.ts)")
	}
}
