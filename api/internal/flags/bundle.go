package flags

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/serverkeys"
)

// TTL is how long a bundle is good for: the client refreshes within it
// and the server's cache keeps one this long (Story 16.6).
const TTL = 60 * time.Second

// Bundle is the body of GET /api/me/flags.
//
// Signature is base64url (no padding) Ed25519 over SignedBytes: the
// JSON object {expires_at, flags, issued_at, kid, tier} with its keys
// sorted at every level, no insignificant whitespace and no HTML
// escaping. Times are RFC 3339 UTC to the second.
type Bundle struct {
	Flags     map[string]any `json:"flags"`
	Tier      string         `json:"tier"`
	Kid       string         `json:"kid"`
	IssuedAt  string         `json:"issued_at"`
	ExpiresAt string         `json:"expires_at"`
	Signature string         `json:"signature"`
}

// Errors from Verify.
var (
	ErrUnsigned = errors.New("flags: bundle is not signed")
	ErrExpired  = errors.New("flags: bundle has expired")
)

// SignedBytes returns the canonical bytes the signature covers.
func (b *Bundle) SignedBytes() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// encoding/json writes map keys sorted, which is the canonical form.
	err := enc.Encode(map[string]any{
		"expires_at": b.ExpiresAt,
		"flags":      b.Flags,
		"issued_at":  b.IssuedAt,
		"kid":        b.Kid,
		"tier":       b.Tier,
	})
	if err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// sign stamps the bundle's times and kid and signs it with keys.
func (b *Bundle) sign(keys *serverkeys.Store, now time.Time) error {
	b.IssuedAt = now.UTC().Format(time.RFC3339)
	b.ExpiresAt = now.Add(TTL).UTC().Format(time.RFC3339)
	b.Kid = keys.Active().Kid
	payload, err := b.SignedBytes()
	if err != nil {
		return err
	}
	sig, kid := keys.Sign(payload)
	if kid != b.Kid {
		// a rotation between Active and Sign: sign again under the new kid
		b.Kid = kid
		if payload, err = b.SignedBytes(); err != nil {
			return err
		}
		sig, _ = keys.Sign(payload)
	}
	b.Signature = base64.RawURLEncoding.EncodeToString(sig)
	return nil
}

// Verify checks b's signature against keys (the active key, or the
// previous one inside its rotation overlap) and that it has not
// expired at now. It is what a privileged action that trusts a kept
// bundle runs first (Story 16.6 EC: a tampered cache is refused).
func Verify(b *Bundle, keys *serverkeys.Store, now time.Time) error {
	if b == nil || b.Signature == "" || b.Kid == "" {
		return ErrUnsigned
	}
	sig, err := base64.RawURLEncoding.DecodeString(b.Signature)
	if err != nil {
		return serverkeys.ErrBadSig
	}
	payload, err := b.SignedBytes()
	if err != nil {
		return err
	}
	if err := keys.Verify(payload, sig, b.Kid); err != nil {
		return err
	}
	exp, err := time.Parse(time.RFC3339, b.ExpiresAt)
	if err != nil || !now.Before(exp) {
		return ErrExpired
	}
	return nil
}
