// Package token mints and verifies short-lived access tokens.
//
// Format is a compact HMAC-SHA256-signed JSON envelope so we avoid the
// jwt-go dependency tree. The shape is intentionally JWT-compatible
// (base64url(header).base64url(payload).base64url(sig)) so SPAs and
// mobile clients can read fields with standard libraries.
package token

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AccessTTL is the access token lifetime. Short enough that revocation
// via refresh-token rotation is responsive; long enough to avoid the
// SPA needing to refresh more than once per minute under normal load.
const AccessTTL = 15 * time.Minute

// Issuer is the `iss` claim baked into every token. Verifiers reject
// tokens with a different issuer.
const Issuer = "https://api.maktaba.app"

var (
	ErrMalformed = errors.New("token: malformed")
	ErrSignature = errors.New("token: bad signature")
	ErrExpired   = errors.New("token: expired")
	ErrIssuer    = errors.New("token: wrong issuer")
)

// Claims is the JSON payload. Field names match RFC 7519 + the
// Maktaba-specific `plan` field used by client UIs to gate Pro features
// without a separate API call.
type Claims struct {
	Iss   string `json:"iss"`
	Sub   string `json:"sub"`           // user id (UUID)
	Email string `json:"email,omitempty"`
	Plan  string `json:"plan,omitempty"`
	IAT   int64  `json:"iat"`
	EXP   int64  `json:"exp"`
	JTI   string `json:"jti"`
}

// Signer issues + verifies tokens. The HMAC secret is loaded once at
// boot — rotation requires a process restart, which is acceptable
// because access tokens expire in 15 min anyway.
type Signer struct {
	secret []byte
}

func NewSigner(secret []byte) *Signer {
	return &Signer{secret: secret}
}

// Issue returns the encoded token string.
func (s *Signer) Issue(userID, email, plan string, now time.Time) (string, error) {
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", err
	}
	c := Claims{
		Iss:   Issuer,
		Sub:   userID,
		Email: email,
		Plan:  plan,
		IAT:   now.Unix(),
		EXP:   now.Add(AccessTTL).Unix(),
		JTI:   base64.RawURLEncoding.EncodeToString(jti),
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	signing := header + "." + payload
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(signing))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signing + "." + sig, nil
}

// Verify validates the signature, expiration, and issuer, returning
// the parsed claims. The function does not consult the DB — revocation
// happens on the refresh-token side.
func (s *Signer) Verify(tok string, now time.Time) (Claims, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return Claims{}, ErrMalformed
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	wantSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(wantSig), []byte(parts[2])) {
		return Claims{}, ErrSignature
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrMalformed
	}
	var c Claims
	if err := json.Unmarshal(body, &c); err != nil {
		return Claims{}, ErrMalformed
	}
	if c.Iss != Issuer {
		return Claims{}, ErrIssuer
	}
	if time.Unix(c.EXP, 0).Before(now) {
		return Claims{}, ErrExpired
	}
	return c, nil
}

// SecretFromEnv reads the HMAC secret. Caller is responsible for
// surfacing a useful error when the env var is missing.
func SecretFromEnv(envVal string) ([]byte, error) {
	if envVal == "" {
		return nil, fmt.Errorf("token: secret env var unset")
	}
	if len(envVal) < 32 {
		return nil, fmt.Errorf("token: secret must be >= 32 bytes")
	}
	return []byte(envVal), nil
}
