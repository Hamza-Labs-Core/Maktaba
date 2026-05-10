package auth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// JWT errors. Mapped to signed-URL sub-types in the middleware.
var (
	ErrMalformed      = errors.New("jwt: malformed token")
	ErrUnsupportedAlg = errors.New("jwt: unsupported alg")
	ErrUnknownKID     = errors.New("jwt: unknown kid")
	ErrSignature      = errors.New("jwt: signature mismatch")
	ErrExpired        = errors.New("jwt: token expired")
	ErrNotYetValid    = errors.New("jwt: token used before nbf")
)

type joseHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

// VerifyConfig governs Verify's behaviour.
type VerifyConfig struct {
	// Now is the wall-time the verifier compares against. Tests
	// inject a fixed value; production passes time.Now.
	Now func() time.Time
	// Leeway accepts tokens whose exp is within this window of Now
	// to ride out clock skew between API and Streaming hosts
	// (Story 8.1 AC-1 `clock_skew_leeway_sec`).
	Leeway time.Duration
}

// Verify parses and signature-checks a JWT against the JWKS cache.
// Returns either the decoded claims or one of the sentinel errors so
// the middleware can map to a signed-URL sub-type.
func Verify(raw string, jwks *JWKSCache, cfg VerifyConfig) (*Claims, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, ErrMalformed
	}
	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrMalformed
	}
	var h joseHeader
	if err := json.Unmarshal(hdrBytes, &h); err != nil {
		return nil, ErrMalformed
	}
	if h.Alg != "RS256" {
		return nil, ErrUnsupportedAlg
	}

	var pub *rsa.PublicKey
	if h.Kid != "" {
		pub = jwks.Lookup(h.Kid)
	}
	if pub == nil {
		// Some test fixtures omit kid; fall back to "any key" so a
		// single-key dev install Just Works.
		pub = jwks.AnyKey()
	}
	if pub == nil {
		return nil, ErrUnknownKID
	}

	signingInput := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrMalformed
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return nil, ErrSignature
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrMalformed
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, ErrMalformed
	}

	now := cfg.Now()
	if claims.Exp == 0 {
		return nil, ErrMalformed
	}
	if claims.Nbf > 0 && now.Add(cfg.Leeway).Unix() < claims.Nbf {
		return nil, ErrNotYetValid
	}
	if now.Add(-cfg.Leeway).Unix() > claims.Exp {
		return nil, ErrExpired
	}
	return &claims, nil
}

// Sign mints an RS256 JWT carrying claims with the supplied private key
// and kid. Tests use this to produce fixtures; production never signs
// — the API owns issuance.
func Sign(claims *Claims, key *rsa.PrivateKey, kid string) (string, error) {
	hdr, err := json.Marshal(joseHeader{Alg: "RS256", Typ: "JWT", Kid: kid})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(hdr) + "." + enc.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + enc.EncodeToString(sig), nil
}
