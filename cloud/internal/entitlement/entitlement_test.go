package entitlement

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

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
