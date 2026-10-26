package entitlement

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSignVerify_RoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s := &Signer{Priv: priv, Fp: Fingerprint(pub)}
	tok, err := s.Sign(Token{
		UserID:    "user-1",
		Plan:      "pro",
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, err := Verify(tok, pub, time.Now())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.UserID != "user-1" || got.Plan != "pro" {
		t.Errorf("claims: %+v", got)
	}
	if got.KeyFp == "" {
		t.Errorf("KeyFp should be populated")
	}
}

func TestVerify_RejectsExpired(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s := &Signer{Priv: priv, Fp: Fingerprint(pub)}
	tok, _ := s.Sign(Token{UserID: "u", Plan: "free", ExpiresAt: time.Now().Add(-time.Second)})
	if _, err := Verify(tok, pub, time.Now()); err == nil {
		t.Error("Verify(expired) should fail")
	}
}

func TestVerify_RejectsWrongKey(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s := &Signer{Priv: priv, Fp: Fingerprint(pub)}
	tok, _ := s.Sign(Token{UserID: "u", Plan: "free", ExpiresAt: time.Now().Add(time.Hour)})
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Verify(tok, otherPub, time.Now()); err == nil {
		t.Error("Verify(wrong key) should fail")
	}
}

func TestFingerprintShape(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	fp := Fingerprint(pub)
	if _, err := hex.DecodeString(fp); err != nil {
		t.Errorf("fingerprint not hex: %v", err)
	}
	if len(fp) != 16 {
		t.Errorf("fingerprint length = %d, want 16", len(fp))
	}
}

// A seed written as hex (openssl rand -hex 32) is 64 characters — the
// expanded-key length — and must still be read as hex.
func TestLoadSignerHexSeed(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	p := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(p, []byte(hex.EncodeToString(seed)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSignerFromFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Public().Equal(ed25519.NewKeyFromSeed(seed).Public()) {
		t.Fatal("hex seed read as raw bytes")
	}
}
