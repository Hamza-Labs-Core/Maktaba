// Package entitlement signs and verifies user/server entitlement
// tokens. On-prem servers verify these tokens locally to unlock paid
// features without a round-trip to the cloud on every action.
//
// Token format:
//   <base64url(payload)>.<base64url(ed25519 sig)>
// Payload is JSON. We do NOT use the JWT layout here: callers on the
// server side are tiny embedded binaries (Raspberry Pi, NAS) and the
// extra parsing complexity of JWT isn't worth it.
package entitlement

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Token claims. ServerID may be empty (user-scope entitlement).
type Token struct {
	UserID    string    `json:"sub"`
	ServerID  string    `json:"srv,omitempty"`
	Plan      string    `json:"plan"`
	IssuedAt  time.Time `json:"iat"`
	ExpiresAt time.Time `json:"exp"`
	KeyFp     string    `json:"kfp"`
}

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
	b = []byte(strings.TrimSpace(string(b)))
	// Allow PEM-style or raw hex/base64.
	var key ed25519.PrivateKey
	switch {
	case len(b) == ed25519.PrivateKeySize:
		key = ed25519.PrivateKey(b)
	case len(b) == ed25519.SeedSize:
		key = ed25519.NewKeyFromSeed(b)
	default:
		if decoded, err := base64.StdEncoding.DecodeString(string(b)); err == nil {
			switch len(decoded) {
			case ed25519.PrivateKeySize:
				key = ed25519.PrivateKey(decoded)
			case ed25519.SeedSize:
				key = ed25519.NewKeyFromSeed(decoded)
			}
		}
		if decoded, err := hex.DecodeString(string(b)); err == nil {
			switch len(decoded) {
			case ed25519.PrivateKeySize:
				key = ed25519.PrivateKey(decoded)
			case ed25519.SeedSize:
				key = ed25519.NewKeyFromSeed(decoded)
			}
		}
	}
	if key == nil {
		return nil, errors.New("entitlement: unable to parse private key (need 32-byte seed or 64-byte ed25519 key)")
	}
	pub := key.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	return &Signer{Priv: key, Fp: hex.EncodeToString(sum[:8])}, nil
}

// Sign returns the encoded token string.
func (s *Signer) Sign(t Token) (string, error) {
	t.KeyFp = s.Fp
	payload, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	b64 := base64.RawURLEncoding.EncodeToString(payload)
	sig := ed25519.Sign(s.Priv, []byte(b64))
	return b64 + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify checks a token against a known public key. Returns the claim
// set on success.
func Verify(tok string, pub ed25519.PublicKey, now time.Time) (Token, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 2 {
		return Token{}, errors.New("entitlement: malformed token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Token{}, errors.New("entitlement: bad signature encoding")
	}
	if !ed25519.Verify(pub, []byte(parts[0]), sig) {
		return Token{}, errors.New("entitlement: signature mismatch")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Token{}, err
	}
	var t Token
	if err := json.Unmarshal(payload, &t); err != nil {
		return Token{}, err
	}
	if now.After(t.ExpiresAt) {
		return Token{}, errors.New("entitlement: expired")
	}
	return t, nil
}

// Fingerprint returns the truncated SHA-256 of a public key — what we
// embed in the kfp claim and store in entitlement_keys.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// String is a tiny debug helper.
func (t Token) String() string {
	return fmt.Sprintf("entitlement{sub=%s srv=%s plan=%s exp=%s kfp=%s}", t.UserID, t.ServerID, t.Plan, t.ExpiresAt.Format(time.RFC3339), t.KeyFp)
}
