// Package entitlement holds the cloud's Ed25519 entitlement key: the
// license-signing key (package license signs the licenses homes verify,
// and release builds of the home embed its public half) from which the
// home-assertion key is also derived.
//
// It used to sign a second, user-scoped token format
// (GET /v1/billing/entitlement) that no home ever read; Epic 16 kept one
// format, the home's license, and that token is gone.
package entitlement

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strings"
)

// Signer wraps the private key.
type Signer struct {
	Priv ed25519.PrivateKey
	Fp   string
}

// LoadSignerFromFile reads a raw ed25519 private key (32-byte seed or
// 64-byte expanded form) from disk. Both forms are accepted so ops
// teams can use whichever their KMS exports.
func LoadSignerFromFile(path string) (*Signer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseSigner(b)
}

// ParseSigner reads a key in any form LoadSignerFromFile accepts (the
// operator CLI pipes one in from a keychain, never through a file).
func ParseSigner(b []byte) (*Signer, error) {
	b = []byte(strings.TrimSpace(string(b)))
	// Allow PEM-style or raw hex/base64.
	// Text encodings first: 64 hex characters are also 64 bytes, and
	// reading them raw as an expanded key would publish a garbage
	// public key.
	var key ed25519.PrivateKey
	fromBytes := func(d []byte) ed25519.PrivateKey {
		switch len(d) {
		case ed25519.PrivateKeySize:
			return ed25519.PrivateKey(d)
		case ed25519.SeedSize:
			return ed25519.NewKeyFromSeed(d)
		}
		return nil
	}
	if decoded, err := hex.DecodeString(string(b)); err == nil {
		key = fromBytes(decoded)
	}
	if key == nil {
		if decoded, err := base64.StdEncoding.DecodeString(string(b)); err == nil {
			key = fromBytes(decoded)
		}
	}
	if key == nil {
		key = fromBytes(b)
	}
	if key == nil {
		return nil, errors.New("entitlement: unable to parse private key (need 32-byte seed or 64-byte ed25519 key)")
	}
	pub := key.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	return &Signer{Priv: key, Fp: hex.EncodeToString(sum[:8])}, nil
}

// Fingerprint returns the truncated SHA-256 of a public key — what we
// embed in the kfp claim and store in entitlement_keys.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// Public returns the verification key home servers pin.
func (s *Signer) Public() ed25519.PublicKey { return s.Priv.Public().(ed25519.PublicKey) }

// RegisterKey records the signer's public key in entitlement_keys so
// grants can reference it and operators can see which key is live.
// Idempotent; called at api start.
func RegisterKey(ctx context.Context, db *sql.DB, s *Signer) error {
	_, err := db.ExecContext(ctx, `
        INSERT INTO entitlement_keys (fingerprint, public_key) VALUES ($1, $2)
        ON CONFLICT (fingerprint) DO NOTHING
    `, s.Fp, []byte(s.Public()))
	return err
}
