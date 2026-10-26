// Package homeassert signs the cloud's home assertions (Epic 42 story
// 42.7, linked accounts): a short-lived, one-use statement, signed by
// the cloud, that a cloud account is signed in and asks one home for
// something. A home verifies it with the cloud's public key (pinned at
// claim) and decides everything else itself: an assertion names who,
// never what they may do.
//
// Token:
//
//	mkha1.<base64url(payload JSON)>.<base64url(ed25519 signature)>
//
// The signature covers SigningPrefix + "." + <base64url(payload)>, so a
// signature made for another purpose of the same key (or another key
// format) can never pass as an assertion.
//
// The home's verifier is api/internal/auth/cloudassert (the cloud is a
// self-contained module); both check the golden vector in their tests.
package homeassert

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	// Prefix starts every assertion token.
	Prefix = "mkha1."
	// SigningPrefix is the domain separator the signature covers.
	SigningPrefix = "maktaba-home-assertion-v1"
	// Type is the payload's typ.
	Type = "home-assertion"
	// TTL is an assertion's lifetime: one use, a minute.
	TTL = time.Minute
)

// Purposes. A home accepts each only where it means it: signin at
// POST /api/auth/cloud, link at its Account page's link step.
const (
	PurposeSignIn = "signin"
	PurposeLink   = "link"
)

// Payload is the signed statement.
type Payload struct {
	Typ string `json:"typ"`
	Pur string `json:"pur"`
	// Sub is the cloud user's id.
	Sub string `json:"sub"`
	// Aud is the home's server id (the cloud's servers.id).
	Aud string `json:"aud"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
	Jti string `json:"jti"`
	// Non is the home's link nonce (purpose link only): it binds the
	// assertion to the home user who started the link.
	Non string `json:"non,omitempty"`
	// Lbl is a label for the cloud account the home shows on its
	// Account page (purpose link only).
	Lbl string `json:"lbl,omitempty"`
}

// Signer holds the cloud's assertion key.
type Signer struct {
	priv ed25519.PrivateKey
	now  func() time.Time
}

// keyDomain separates the assertion key from the entitlement key it is
// derived from.
const keyDomain = "maktaba-home-assertion-key-v1"

// DeriveFromSeed derives the assertion key from another Ed25519 seed (the
// entitlement key's), so no second secret has to be provisioned and
// backed up: seed' = SHA-256(keyDomain || seed). The two keys are
// unrelated to anyone without the seed.
func DeriveFromSeed(seed []byte) *Signer {
	h := sha256.New()
	h.Write([]byte(keyDomain))
	h.Write(seed)
	return NewSigner(ed25519.NewKeyFromSeed(h.Sum(nil)))
}

// NewSigner wraps a private key.
func NewSigner(priv ed25519.PrivateKey) *Signer {
	return &Signer{priv: priv, now: time.Now}
}

// PublicKey is what a home pins.
func (s *Signer) PublicKey() ed25519.PublicKey { return s.priv.Public().(ed25519.PublicKey) }

// PublicKeyB64 is the public key as a home stores it (standard base64).
func (s *Signer) PublicKeyB64() string {
	return base64.StdEncoding.EncodeToString(s.PublicKey())
}

// Issue signs a fresh assertion: purpose, cloud user, home; nonce and
// label for a link.
func (s *Signer) Issue(purpose, cloudUserID, serverID, nonce, label string) (string, Payload, error) {
	if purpose != PurposeSignIn && purpose != PurposeLink {
		return "", Payload{}, errors.New("homeassert: unknown purpose")
	}
	if cloudUserID == "" || serverID == "" {
		return "", Payload{}, errors.New("homeassert: cloud user and home are required")
	}
	if purpose == PurposeLink && nonce == "" {
		return "", Payload{}, errors.New("homeassert: a link needs the home's nonce")
	}
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", Payload{}, err
	}
	now := s.now().UTC()
	p := Payload{Typ: Type, Pur: purpose, Sub: cloudUserID, Aud: serverID,
		Iat: now.Unix(), Exp: now.Add(TTL).Unix(), Jti: hex.EncodeToString(jti)}
	if purpose == PurposeLink {
		p.Non, p.Lbl = nonce, label
	}
	tok, err := s.Sign(p)
	return tok, p, err
}

// Sign encodes and signs p as is (Issue fills it; tests sign vectors).
func (s *Signer) Sign(p Payload) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	b := base64.RawURLEncoding.EncodeToString(raw)
	sig := ed25519.Sign(s.priv, []byte(SigningPrefix+"."+b))
	return Prefix + b + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify checks tok's signature and shape (not its purpose, home or
// one-use: those are the home's). The cloud uses it only in tests.
func Verify(tok string, pub ed25519.PublicKey, now time.Time) (Payload, error) {
	rest, ok := strings.CutPrefix(tok, Prefix)
	if !ok {
		return Payload{}, errors.New("homeassert: not an assertion")
	}
	b, sigB, ok := strings.Cut(rest, ".")
	if !ok {
		return Payload{}, errors.New("homeassert: malformed")
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB)
	if err != nil || !ed25519.Verify(pub, []byte(SigningPrefix+"."+b), sig) {
		return Payload{}, errors.New("homeassert: bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(b)
	if err != nil {
		return Payload{}, errors.New("homeassert: malformed")
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil || p.Typ != Type {
		return Payload{}, errors.New("homeassert: malformed")
	}
	if now.Unix() > p.Exp {
		return Payload{}, errors.New("homeassert: expired")
	}
	return p, nil
}
