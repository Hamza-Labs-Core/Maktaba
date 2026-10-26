// Package cloudlinktest signs home assertions the way the cloud does
// (cloud/internal/homeassert), for the home's tests.
package cloudlinktest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/cloudlink"
)

// Cloud is a fake Maktaba Cloud: a key and the home's server id.
type Cloud struct {
	Priv     ed25519.PrivateKey
	ServerID string
	// IdentityPath is the identity file it wrote (as the relay agent does).
	IdentityPath string
}

// New makes a cloud with a fresh key and writes the home's identity file
// in a temp dir.
func New(t *testing.T) *Cloud {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := &Cloud{Priv: priv, ServerID: "6f1c2a8e-0000-4000-8000-000000000001",
		IdentityPath: filepath.Join(t.TempDir(), "cloudlink.identity.json")}
	b, _ := json.Marshal(cloudlink.Identity{ServerID: c.ServerID, Slug: "home", CloudAPI: "https://cloud.test",
		AssertionKey: base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))})
	if err := os.WriteFile(c.IdentityPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return c
}

// Sign signs a as is.
func (c *Cloud) Sign(a cloudlink.Assertion) string {
	raw, _ := json.Marshal(a)
	b := base64.RawURLEncoding.EncodeToString(raw)
	sig := ed25519.Sign(c.Priv, []byte("maktaba-home-assertion-v1."+b))
	return "mkha1." + b + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// Assertion is a fresh assertion for this home: purpose, cloud user, and a
// link's nonce.
func (c *Cloud) Assertion(purpose, cloudUserID, nonce string) string {
	j := make([]byte, 8)
	_, _ = rand.Read(j)
	now := time.Now().Unix()
	return c.Sign(cloudlink.Assertion{Typ: "home-assertion", Pur: purpose, Sub: cloudUserID, Aud: c.ServerID,
		Iat: now, Exp: now + 60, Jti: hex.EncodeToString(j), Non: nonce})
}
