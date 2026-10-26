// Package cloudlink is the home's side of linked accounts (Epic 42 story
// 42.7): a home user links a Maktaba Cloud account, and that account can
// then sign in to this home through the cloud. The cloud only vouches
// for who is signing in (a one-use assertion it signs); this home finds
// the home user it linked, issues its own tokens with that user's role
// capped by the link's scope, and refuses everything else.
package cloudlink

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// The assertion format, as cloud/internal/homeassert signs it:
//
//	mkha1.<base64url(payload JSON)>.<base64url(ed25519 signature)>
//
// The signature covers SigningPrefix + "." + <base64url(payload)>. Both
// ends check the same golden vector in their tests.
const (
	assertionPrefix = "mkha1."
	signingPrefix   = "maktaba-home-assertion-v1"
	assertionType   = "home-assertion"

	// PurposeSignIn is an assertion for POST /api/auth/cloud.
	PurposeSignIn = "signin"
	// PurposeLink is an assertion for the Account page's link step.
	PurposeLink = "link"

	// maxAssertionLife bounds exp - iat whatever the cloud wrote (it
	// signs a minute); clockSkew is the leeway on both ends.
	maxAssertionLife = 2 * time.Minute
	clockSkew        = 30 * time.Second
)

// Assertion is a verified assertion's payload.
type Assertion struct {
	Typ string `json:"typ"`
	Pur string `json:"pur"`
	Sub string `json:"sub"` // the cloud user's id
	Aud string `json:"aud"` // the home's server id in the cloud
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
	Jti string `json:"jti"`
	Non string `json:"non,omitempty"`
	Lbl string `json:"lbl,omitempty"`
}

// ErrAssertion is every way an assertion fails verification; the reason
// is for the log, never the response (no oracle).
var ErrAssertion = errors.New("cloudlink: assertion refused")

func refuse(reason string) error { return errors.Join(ErrAssertion, errors.New(reason)) }

// VerifyAssertion checks an assertion's signature against the pinned key,
// its purpose, that it names this home, and its lifetime. One use is the
// store's (UseAssertion).
func VerifyAssertion(tok string, pub ed25519.PublicKey, purpose, serverID string, now time.Time) (Assertion, error) {
	if len(pub) != ed25519.PublicKeySize {
		return Assertion{}, refuse("no pinned cloud key")
	}
	if len(tok) > 4096 {
		return Assertion{}, refuse("too long")
	}
	rest, ok := strings.CutPrefix(tok, assertionPrefix)
	if !ok {
		return Assertion{}, refuse("not an assertion")
	}
	b, sigB, ok := strings.Cut(rest, ".")
	if !ok {
		return Assertion{}, refuse("malformed")
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigB)
	if err != nil || !ed25519.Verify(pub, []byte(signingPrefix+"."+b), sig) {
		return Assertion{}, refuse("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(b)
	if err != nil {
		return Assertion{}, refuse("malformed payload")
	}
	var a Assertion
	if err := json.Unmarshal(raw, &a); err != nil || a.Typ != assertionType {
		return Assertion{}, refuse("malformed payload")
	}
	switch {
	case a.Pur != purpose:
		return Assertion{}, refuse("wrong purpose")
	case serverID == "" || a.Aud != serverID:
		return Assertion{}, refuse("for another home")
	case a.Sub == "" || a.Jti == "":
		return Assertion{}, refuse("incomplete")
	case a.Exp < a.Iat || time.Duration(a.Exp-a.Iat)*time.Second > maxAssertionLife:
		return Assertion{}, refuse("lifetime")
	case now.Add(clockSkew).Unix() < a.Iat:
		return Assertion{}, refuse("not yet valid")
	case now.Add(-clockSkew).Unix() > a.Exp:
		return Assertion{}, refuse("expired")
	case purpose == PurposeLink && a.Non == "":
		return Assertion{}, refuse("link without nonce")
	}
	return a, nil
}

// ParsePublicKey decodes a pinned key (standard base64 Ed25519).
func ParsePublicKey(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("cloudlink: the pinned cloud key is not an Ed25519 key")
	}
	return ed25519.PublicKey(raw), nil
}
