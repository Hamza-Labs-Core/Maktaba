// Package jwt is a small RS256-only JWT issuer/verifier on top of
// internal/auth/keys.
//
// We deliberately do not pull in a third-party JWT library: every
// dependency we add to the auth path becomes an attack surface, and
// the JWT spec we actually use is small enough to write straight
// against the stdlib. RS256 only; HS256 is not supported (and never
// will be in this package — the README's "alg: none" warning applies).
package jwt

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
)

// Claims is the canonical Maktaba access-token shape (Epic 10
// README §"Streaming JWT shape").
type Claims struct {
	Iss     string   `json:"iss"`
	Aud     string   `json:"aud"`
	Sub     string   `json:"sub"`
	Iat     int64    `json:"iat"`
	Exp     int64    `json:"exp"`
	Jti     string   `json:"jti"`
	Usr     string   `json:"usr,omitempty"`
	Lib     []string `json:"lib,omitempty"`
	IsAdmin bool     `json:"is_admin,omitempty"`
}

// Errors exported by Verify.
var (
	ErrMalformed      = errors.New("jwt: malformed token")
	ErrUnsupportedAlg = errors.New("jwt: unsupported alg")
	ErrUnknownKID     = errors.New("jwt: unknown kid")
	ErrSignature      = errors.New("jwt: signature mismatch")
	ErrExpired        = errors.New("jwt: token expired")
	ErrNotYetValid    = errors.New("jwt: token used before iat")
	ErrAudience       = errors.New("jwt: audience mismatch")
)

// header is the JOSE header for a Maktaba token. Only RS256 is
// emitted; only RS256 is accepted on verify.
type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	KID string `json:"kid"`
}

// Sign produces a JWT carrying `c`, signed with the active key in
// `set`. `c.Iat`, `c.Exp`, and `c.Jti` are filled in if not already
// set: callers usually let the issuer pick.
func Sign(set *keys.Set, c Claims) (string, error) {
	k := set.Active()
	if k == nil {
		return "", errors.New("jwt: no active signing key")
	}
	now := time.Now().UTC()
	if c.Iat == 0 {
		c.Iat = now.Unix()
	}
	if c.Exp == 0 {
		// 15 min default for access tokens (matches Story 10.3).
		c.Exp = now.Add(15 * time.Minute).Unix()
	}
	if c.Jti == "" {
		c.Jti = randomJTI()
	}

	hdrJSON, err := json.Marshal(header{Alg: "RS256", Typ: "JWT", KID: k.KID})
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(c)
	if err != nil {
		return "", err
	}

	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(hdrJSON) + "." + enc.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k.Private, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + enc.EncodeToString(sig), nil
}

// Verify parses, signature-checks, and time-validates `tok` against
// the trust set. Returns the decoded claims on success.
//
// `audience` is the expected `aud` value (empty string ⇒ skip the
// audience check). The expiry/iat checks allow 30s of clock skew.
func Verify(set *keys.Set, tok, audience string) (*Claims, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, ErrMalformed
	}
	enc := base64.RawURLEncoding
	hdrBytes, err := enc.DecodeString(parts[0])
	if err != nil {
		return nil, ErrMalformed
	}
	var hdr header
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		return nil, ErrMalformed
	}
	if hdr.Alg != "RS256" {
		return nil, ErrUnsupportedAlg
	}
	if hdr.KID == "" {
		return nil, ErrMalformed
	}
	k := set.FindByKID(hdr.KID)
	if k == nil {
		return nil, ErrUnknownKID
	}
	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		return nil, ErrMalformed
	}
	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(k.Public, crypto.SHA256, digest[:], sig); err != nil {
		return nil, ErrSignature
	}

	claimBytes, err := enc.DecodeString(parts[1])
	if err != nil {
		return nil, ErrMalformed
	}
	var c Claims
	if err := json.Unmarshal(claimBytes, &c); err != nil {
		return nil, ErrMalformed
	}
	now := time.Now().UTC().Unix()
	if c.Exp != 0 && now > c.Exp+30 {
		return nil, ErrExpired
	}
	if c.Iat != 0 && now+30 < c.Iat {
		return nil, ErrNotYetValid
	}
	if audience != "" && c.Aud != audience {
		return nil, ErrAudience
	}
	return &c, nil
}

// randomJTI returns a 128-bit random hex string. Callers can override
// by setting Claims.Jti before Sign.
func randomJTI() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x", b)
}
