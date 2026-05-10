// Package keys owns the RS256 key material used to mint and verify
// Maktaba access tokens (Story 10.6).
//
// Two responsibilities live here:
//
//  1. Loading keys from PEM strings (env vars or operator-controlled
//     files). The `kid` is derived deterministically as
//     `sha256(public-DER)` truncated to 16 hex chars, so the same key
//     produces the same kid across processes.
//
//  2. The trust set (`Set`) — the active signing key plus any
//     overlap-window keys whose tokens are still valid. `Set.JWKS()`
//     marshals the public side as a JWKS document for AC-3.
//
// Mid-process rotation is a swap on `Set`: the new key becomes
// active, the old one slides to `previous`. After
// `rotation_overlap_sec` the previous slot is cleared.
package keys

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

// MinBits is the smallest RSA modulus Maktaba accepts. Story 10.6
// EC-2 explicitly refuses keys shorter than this so a misconfigured
// install doesn't silently degrade JWT security.
const MinBits = 2048

// DefaultBits is what `keys init` generates (Story 10.6 AC-2).
const DefaultBits = 4096

// DefaultRotationOverlap is the default `rotation_overlap_sec` from
// AC-4 — 24 hours of overlap so in-flight tokens keep working through
// a routine rotation.
const DefaultRotationOverlap = 24 * time.Hour

// Key wraps an RSA private/public pair plus the derived kid. Keys
// are immutable once constructed; rotation builds new Key values.
type Key struct {
	KID     string
	Private *rsa.PrivateKey
	Public  *rsa.PublicKey

	// AddedAt is the wall-clock time the key entered the trust set.
	// Used by the overlap reaper to decide when a previous-key slot
	// should be cleared.
	AddedAt time.Time
}

// Generate creates a new RSA keypair of `bits` size and wraps it as a
// Key. The kid is derived from the public key DER so re-loading the
// same PEM produces the same kid.
func Generate(bits int) (*Key, error) {
	if bits < MinBits {
		return nil, fmt.Errorf("keys: refusing to generate %d-bit key (minimum %d)", bits, MinBits)
	}
	priv, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, err
	}
	return wrap(priv)
}

// FromPEM parses a private-key PEM and a public-key PEM, asserts
// internal consistency, and returns the Key.
//
// Either RSA PRIVATE KEY (PKCS#1) or PRIVATE KEY (PKCS#8) blocks are
// accepted on the private side; PUBLIC KEY (PKIX) on the public side.
// The public key MUST match the private one — otherwise the operator
// has crossed wires between two installs and we abort at boot rather
// than mint tokens nobody can verify.
func FromPEM(privatePEM, publicPEM string) (*Key, error) {
	priv, err := parsePrivatePEM(privatePEM)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	pub, err := parsePublicPEM(publicPEM)
	if err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}
	if pub.N.Cmp(priv.N) != 0 || pub.E != priv.E {
		return nil, errors.New("public key does not match private key")
	}
	if priv.N.BitLen() < MinBits {
		return nil, fmt.Errorf("keys: refusing to load %d-bit key (minimum %d)", priv.N.BitLen(), MinBits)
	}
	return wrap(priv)
}

func wrap(priv *rsa.PrivateKey) (*Key, error) {
	kid, err := computeKID(&priv.PublicKey)
	if err != nil {
		return nil, err
	}
	return &Key{
		KID:     kid,
		Private: priv,
		Public:  &priv.PublicKey,
		AddedAt: time.Now().UTC(),
	}, nil
}

// computeKID returns sha256(public-DER) truncated to the first 16 hex
// chars, which gives a 64-bit value that's ample for collision
// resistance across the lifetime of an install (Story 10.6 AC-1).
func computeKID(pub *rsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])[:16], nil
}

func parsePrivatePEM(in string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(in))
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("PKCS#8 key is not RSA")
		}
		return rk, nil
	}
	return nil, fmt.Errorf("unsupported PEM block type %q", block.Type)
}

func parsePublicPEM(in string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(in))
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	if block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("unsupported PEM block type %q", block.Type)
	}
	k, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("public key is not RSA")
	}
	return rk, nil
}

// Set is the live trust store: an active signing key, plus an
// optional previous key kept around during the rotation overlap
// (Story 10.6 AC-4). All accessors are safe for concurrent use.
type Set struct {
	mu       sync.RWMutex
	active   *Key
	previous *Key
	overlap  time.Duration

	// changed is closed and replaced by Rotate so subscribers
	// (Streaming via the LISTEN channel; the SPA via long-polling)
	// can wake up immediately rather than waiting for the next 5-min
	// JWKS refresh.
	changed chan struct{}
}

// NewSet creates an empty Set with the given overlap window. Pass
// DefaultRotationOverlap unless config says otherwise.
func NewSet(overlap time.Duration) *Set {
	if overlap < 0 {
		overlap = 0
	}
	return &Set{overlap: overlap, changed: make(chan struct{})}
}

// Replace seeds the Set with the bootstrap key (typically loaded from
// env at process start). After Replace, Active returns this key and
// Previous is nil.
func (s *Set) Replace(k *Key) {
	s.mu.Lock()
	s.active = k
	s.previous = nil
	old := s.changed
	s.changed = make(chan struct{})
	s.mu.Unlock()
	close(old)
}

// Active returns the current signing key. It is safe to call before
// Replace; the result is nil until the operator has loaded a key.
func (s *Set) Active() *Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

// Previous returns the previous signing key (nil unless we are
// currently inside a rotation overlap window).
func (s *Set) Previous() *Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.previous
}

// All returns active+previous as a slice. Convenience for verifiers.
func (s *Set) All() []*Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []*Key{}
	if s.active != nil {
		out = append(out, s.active)
	}
	if s.previous != nil {
		out = append(out, s.previous)
	}
	return out
}

// FindByKID looks up a key in the trust set. Used by the JWT
// verifier when picking the right RSA modulus to check a signature
// against.
func (s *Set) FindByKID(kid string) *Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.active != nil && s.active.KID == kid {
		return s.active
	}
	if s.previous != nil && s.previous.KID == kid {
		return s.previous
	}
	return nil
}

// Changed returns a channel that closes when the trust set is updated
// (Replace / Rotate). Listeners use this to refresh their JWKS cache.
func (s *Set) Changed() <-chan struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.changed
}

// RotateMode controls Rotate's overlap behaviour. Routine rotations
// keep the previous key trusted for `overlap`; immediate rotations
// invalidate every in-flight token at once (Story 10.6 AC-5).
type RotateMode int

const (
	// RotateRoutine slides the active key into the previous slot and
	// sets the new key as active.
	RotateRoutine RotateMode = iota

	// RotateImmediate clears the previous slot, invalidating every
	// in-flight token signed with any prior key.
	RotateImmediate
)

// Rotate swaps in `next` as the active signing key.
//
//   - RotateRoutine: previous := old-active. The previous slot is
//     reaped after `overlap` by ReapExpired.
//   - RotateImmediate: previous := nil. Every in-flight token is
//     immediately invalid (AC-5).
func (s *Set) Rotate(next *Key, mode RotateMode) {
	if next == nil {
		return
	}
	s.mu.Lock()
	old := s.active
	s.active = next
	if mode == RotateImmediate {
		s.previous = nil
	} else {
		s.previous = old
	}
	oldCh := s.changed
	s.changed = make(chan struct{})
	s.mu.Unlock()
	close(oldCh)
}

// ReapExpired clears the previous-key slot if its overlap has elapsed.
// The serve loop calls this on a 60-s tick — cheaper than scheduling a
// timer per rotation, and the overlap window is measured in hours.
func (s *Set) ReapExpired(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.previous == nil {
		return false
	}
	if now.Sub(s.previous.AddedAt) >= s.overlap {
		s.previous = nil
		return true
	}
	return false
}

// JWKS returns the trust set serialised as a JSON Web Key Set
// (RFC 7517). Story 10.6 AC-3.
func (s *Set) JWKS() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := struct {
		Keys []jwk `json:"keys"`
	}{}
	for _, k := range []*Key{s.active, s.previous} {
		if k == nil {
			continue
		}
		out.Keys = append(out.Keys, toJWK(k))
	}
	return json.Marshal(out)
}

// jwk is the per-key shape inside the JWKS document. Only RS256 is
// supported in v1.
type jwk struct {
	KTY string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	KID string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func toJWK(k *Key) jwk {
	return jwk{
		KTY: "RSA",
		Use: "sig",
		Alg: "RS256",
		KID: k.KID,
		N:   base64URL(k.Public.N.Bytes()),
		E:   base64URL(big.NewInt(int64(k.Public.E)).Bytes()),
	}
}

func base64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// EncodePrivatePEM returns the PEM serialisation of `k`'s private
// component as PKCS#8 — the modern format and the one we recommend
// operators store in env vars.
func EncodePrivatePEM(k *Key) (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k.Private)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

// EncodePublicPEM returns the PEM serialisation of `k`'s public
// component as PKIX.
func EncodePublicPEM(k *Key) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(k.Public)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}
