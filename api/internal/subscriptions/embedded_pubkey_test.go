package subscriptions

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
	"time"
)

// Story 16.4 — "Server bundles license-server public key at build
// time". The verifier must be constructable from an embedded PEM with
// NO injected key, so production wiring (MountP10) no longer leaves
// SubscriptionsVerifier nil. A placeholder/empty embed must fail
// closed (ErrNoEmbeddedKey) — never silently trust a zero key.

func TestParseEd25519PublicKeyPEM_RoundTrip(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	got, err := parseEd25519PublicKeyPEM(pemBytes)
	if err != nil {
		t.Fatalf("parseEd25519PublicKeyPEM: %v", err)
	}
	if !got.Equal(pub) {
		t.Fatal("parsed key does not match original")
	}
}

func TestParseEd25519PublicKeyPEM_RejectsPlaceholder(t *testing.T) {
	cases := map[string][]byte{
		"empty":          {},
		"whitespace":     []byte("   \n\t  \n"),
		"placeholder":    []byte(placeholderPubKeyPEM),
		"not_pem":        []byte("this is not a pem block"),
		"wrong_key_type": rsaLikeGarbagePEM(),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseEd25519PublicKeyPEM(in)
			if err == nil {
				t.Fatal("expected an error for a non-Ed25519/placeholder PEM")
			}
		})
	}
}

func rsaLikeGarbagePEM() []byte {
	return pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: []byte("not-a-real-spki-der"),
	})
}

// EmbeddedVerifier returns ErrNoEmbeddedKey when the build did not
// inject a real key (the default in this repo: the embed file holds
// the documented placeholder). Callers degrade to "license endpoint
// disabled", they do NOT panic and do NOT trust a zero key.
func TestEmbeddedVerifier_PlaceholderYieldsErrNoEmbeddedKey(t *testing.T) {
	v, err := newVerifierFromPEM([]byte(placeholderPubKeyPEM))
	if !errors.Is(err, ErrNoEmbeddedKey) {
		t.Fatalf("err = %v, want ErrNoEmbeddedKey", err)
	}
	if v != nil {
		t.Fatal("verifier must be nil when no embedded key is present")
	}
}

func TestEmbeddedVerifier_RealKeyVerifiesLicense(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	v, err := newVerifierFromPEM(pemBytes)
	if err != nil {
		t.Fatalf("newVerifierFromPEM: %v", err)
	}
	if v == nil || v.PublicKey == nil {
		t.Fatal("expected a usable verifier with a non-nil public key")
	}
	// Sanity: the verifier actually validates a license signed by the
	// matching private key (closes the loop end-to-end).
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	lic, err := Sign(priv, LicenseInner{
		LicenseID: "embed-1", Tier: TierPro, Seats: 0,
		IssuedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(lic, now); err != nil {
		t.Fatalf("Verify with embedded key: %v", err)
	}
}
