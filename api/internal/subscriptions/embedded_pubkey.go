// embedded_pubkey.go bundles the license-server Ed25519 public key into
// the binary at build time (Story 16.4: "Server bundles license-server
// public key at build time").
//
// The gap analysis flagged that the production Verifier was *always*
// nil — MountP10 never supplied SubscriptionsVerifier and there was no
// embedded key — so POST /api/admin/license could never succeed. This
// file closes that: EmbeddedVerifier() returns a Verifier built from
// the embedded SPKI/PEM, or ErrNoEmbeddedKey when the build did not
// inject a real key (the default in this open-source tree, where the
// embed file is a documented placeholder).
//
// Security posture is fail-closed: a missing/placeholder/garbage key
// never yields a usable verifier, so the worst case is "license
// endpoint disabled (503)", never "trust an attacker-chosen key" and
// never a panic.
package subscriptions

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	_ "embed"
	"encoding/pem"
	"errors"
	"fmt"
)

// embeddedPubKeyPEM is the build-time license-server public key. In a
// release build CI overwrites keys/license-server.pub.pem with the real
// SPKI/PEM key; in this tree it holds the placeholder below.
//
//go:embed keys/license-server.pub.pem
var embeddedPubKeyPEM []byte

// placeholderPubKeyPEM is the sentinel marker the placeholder embed
// begins with. parseEd25519PublicKeyPEM treats any input containing
// this marker (or no valid PUBLIC KEY block) as "no key".
const placeholderPubKeyPEM = "-----BEGIN MAKTABA LICENSE-SERVER PUBLIC KEY PLACEHOLDER-----"

// ErrNoEmbeddedKey indicates the build did not bundle a real
// license-server public key. Callers degrade to "license endpoint
// disabled" — they MUST NOT treat this as fatal nor trust a zero key.
var ErrNoEmbeddedKey = errors.New("subscriptions: no embedded license-server public key")

// parseEd25519PublicKeyPEM decodes a PEM-wrapped PKIX/SPKI Ed25519
// public key. It returns ErrNoEmbeddedKey for the documented
// placeholder and for empty/whitespace input; any other malformed
// input is a hard parse error (fail closed).
func parseEd25519PublicKeyPEM(pemBytes []byte) (ed25519.PublicKey, error) {
	trimmed := bytes.TrimSpace(pemBytes)
	if len(trimmed) == 0 {
		return nil, ErrNoEmbeddedKey
	}
	if bytes.Contains(pemBytes, []byte(placeholderPubKeyPEM)) {
		return nil, ErrNoEmbeddedKey
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, errors.New("subscriptions: embedded key is not a PEM PUBLIC KEY block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("subscriptions: parse embedded public key: %w", err)
	}
	ed, ok := pub.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("subscriptions: embedded key is %T, want ed25519.PublicKey", pub)
	}
	return ed, nil
}

// newVerifierFromPEM builds a Verifier from a PEM-encoded Ed25519
// public key. Returns (nil, ErrNoEmbeddedKey) when no real key is
// present so the caller can disable the license endpoint cleanly.
func newVerifierFromPEM(pemBytes []byte) (*Verifier, error) {
	pub, err := parseEd25519PublicKeyPEM(pemBytes)
	if err != nil {
		return nil, err
	}
	return &Verifier{PublicKey: pub}, nil
}

// EmbeddedVerifier returns the Verifier built from the build-time
// embedded license-server public key, or (nil, ErrNoEmbeddedKey) if
// the build bundled only the placeholder. Production wiring
// (router.MountP10) calls this so SubscriptionsVerifier is no longer
// unconditionally nil.
func EmbeddedVerifier() (*Verifier, error) {
	return newVerifierFromPEM(embeddedPubKeyPEM)
}
