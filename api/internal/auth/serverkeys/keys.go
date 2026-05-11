// Package serverkeys owns the long-lived Ed25519 keypair that
// identifies a Maktaba Server to its federation peers, the cloud
// relay, and any consumer that needs to verify signatures
// originating from "this exact box."
//
// The keypair is distinct from the RS256 JWT-signing key in
// `auth/keys` (Story 10.6) — those tokens are short-lived; this
// key is the box's identity for the life of the install.
//
// Story 10.18 in `specs/epics/10-auth-security/` is the spec for
// this package.
package serverkeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
)

// Errors returned by Store methods.
var (
	ErrUnknownKid = errors.New("serverkeys: unknown kid")
	ErrBadSig     = errors.New("serverkeys: signature verification failed")
)

// Key is one identity keypair. PrivateKey is nil for predecessor
// entries that survived a rotation only for verification.
type Key struct {
	Kid        string
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
	CreatedAt  time.Time
	Source     string // "env" | "disk" | "generated"
}

// DeriveKid returns the lowercase hex sha256 of the raw public key
// bytes, truncated to 16 chars. Stable across processes.
func DeriveKid(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])[:16]
}

// Generate produces a fresh Ed25519 keypair with the derived kid.
func Generate(now time.Time, source string) (*Key, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("serverkeys: generate: %w", err)
	}
	return &Key{
		Kid:        DeriveKid(pub),
		PublicKey:  pub,
		PrivateKey: priv,
		CreatedAt:  now,
		Source:     source,
	}, nil
}

// ParsePrivatePEM accepts a PKCS-8 Ed25519 PEM block.
func ParsePrivatePEM(pemBytes []byte, now time.Time, source string) (*Key, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("serverkeys: no PEM block found")
	}
	if block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("serverkeys: expected PRIVATE KEY block, got %q", block.Type)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("serverkeys: parse PKCS-8: %w", err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("serverkeys: PEM does not contain an Ed25519 key (got %T)", parsed)
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("serverkeys: derived public key is not Ed25519")
	}
	return &Key{
		Kid:        DeriveKid(pub),
		PublicKey:  pub,
		PrivateKey: priv,
		CreatedAt:  now,
		Source:     source,
	}, nil
}

// MarshalPrivatePEM emits the PKCS-8 PEM form. Used by the on-disk
// persistence path (after sealing) and by `identity init --print`.
func MarshalPrivatePEM(priv ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("serverkeys: marshal PKCS-8: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// MarshalPublicPEM emits the SubjectPublicKeyInfo PEM form, used by
// the JWKS-style endpoint and any operator export.
func MarshalPublicPEM(pub ed25519.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("serverkeys: marshal SubjectPublicKeyInfo: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}
