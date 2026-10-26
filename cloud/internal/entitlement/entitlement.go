// Package entitlement signs and verifies user/server entitlement
// tokens. On-prem servers verify these tokens locally to unlock paid
// features without a round-trip to the cloud on every action.
//
// Token format:
//
//	<base64url(payload)>.<base64url(ed25519 sig)>
//
// Payload is JSON. We do NOT use the JWT layout here: callers on the
// server side are tiny embedded binaries (Raspberry Pi, NAS) and the
// extra parsing complexity of JWT isn't worth it.
package entitlement

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
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

// TTL is how long an issued entitlement stays valid. Home servers
// refresh well before; a server that cannot reach the cloud keeps its
// paid features this long (offline gate, story 25.26).
const TTL = 7 * 24 * time.Hour

// Issue signs a user-scope entitlement for plan and records the grant.
func Issue(ctx context.Context, db *sql.DB, s *Signer, userID, plan string, now time.Time) (string, Token, error) {
	t := Token{UserID: userID, Plan: plan, IssuedAt: now.UTC(), ExpiresAt: now.UTC().Add(TTL)}
	tok, err := s.Sign(t)
	if err != nil {
		return "", Token{}, err
	}
	t.KeyFp = s.Fp
	if _, err := db.ExecContext(ctx, `
        INSERT INTO entitlement_grants (user_id, plan, issued_at, expires_at, fingerprint)
        VALUES ($1,$2,$3,$4,$5)
    `, userID, plan, t.IssuedAt, t.ExpiresAt, s.Fp); err != nil {
		return "", Token{}, err
	}
	return tok, t, nil
}
