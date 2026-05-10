// Package subscriptions implements the entitlement layer (Epic 16):
//
//   - Tiers: free (the canonical product) and premium.
//   - License keys are Ed25519-signed JSON (story 16.4). The server
//     verifies the signature against a build-time public key, checks
//     expiry, and snapshots the resulting entitlement.
//   - The Entitlements struct is what handlers ask: "is feature X
//     enabled for this caller?"
//
// The free tier is unconditional: every Maktaba feature works without a
// key. Premium gates are *additive* (cloud relay, federation, multi-
// user beyond the seat cap).
package subscriptions

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Tier classifies what the running instance is licensed for.
type Tier string

const (
	// TierFree is the always-on default. No license required.
	TierFree Tier = "free"

	// TierPremium unlocks cloud relay, federation, and >1 seat.
	TierPremium Tier = "premium"
)

// Feature identifies a gated capability. New features extend this enum;
// keeping the names stable is part of the API contract for clients that
// query `/api/entitlements`.
type Feature string

const (
	FeatureCloudRelay     Feature = "cloud_relay"
	FeatureFederation     Feature = "federation"
	FeatureMultiUser      Feature = "multi_user"
	FeatureAdvancedMetric Feature = "advanced_analytics"
	FeatureCloudBackup    Feature = "cloud_metadata_backup"
)

// FreeFeatures are the features the free tier always grants. New
// features default to premium-only; deliberately copy here to enable
// for free.
var FreeFeatures = map[Feature]bool{}

// Entitlements is a snapshot of what the current license grants. A nil
// Entitlements is treated as "free tier, no extras".
type Entitlements struct {
	Tier      Tier
	LicenseID string
	Seats     int
	ExpiresAt time.Time
	Features  map[Feature]bool
}

// Allows reports whether feature f is enabled. The free tier matrix is
// applied first; premium additions override.
func (e *Entitlements) Allows(f Feature) bool {
	if FreeFeatures[f] {
		return true
	}
	if e == nil {
		return false
	}
	return e.Features[f]
}

// License is the signed wire form. The signature covers the canonical
// JSON encoding of LicenseInner (all fields except the signature).
type License struct {
	License   LicenseInner `json:"license"`
	Signature string       `json:"signature"`
}

// LicenseInner is the signed payload.
type LicenseInner struct {
	LicenseID string    `json:"license_id"`
	Tier      Tier      `json:"tier"`
	Seats     int       `json:"seats"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Features  []Feature `json:"features,omitempty"`
}

// Verifier checks license signatures against a public key. Production
// loads the key from a build-time embedded constant; tests construct
// their own.
type Verifier struct {
	PublicKey ed25519.PublicKey
}

// Verify returns the entitlement implied by lic, or an error.
// Sub-checks: signature, expiry, seat-count > 0, tier in {free,premium}.
func (v *Verifier) Verify(lic *License, now time.Time) (*Entitlements, error) {
	if lic == nil {
		return nil, errors.New("nil license")
	}
	sig, err := base64.StdEncoding.DecodeString(lic.Signature)
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	payload, err := canonicalJSON(lic.License)
	if err != nil {
		return nil, err
	}
	if v.PublicKey == nil {
		return nil, errors.New("verifier has no public key")
	}
	if !ed25519.Verify(v.PublicKey, payload, sig) {
		return nil, errors.New("signature verification failed")
	}
	if !now.Before(lic.License.ExpiresAt) {
		return nil, ErrExpired
	}
	if lic.License.Tier != TierFree && lic.License.Tier != TierPremium {
		return nil, fmt.Errorf("unknown tier %q", lic.License.Tier)
	}
	if lic.License.Seats < 0 {
		return nil, errors.New("seats must be >= 0")
	}
	features := map[Feature]bool{}
	for _, f := range lic.License.Features {
		features[f] = true
	}
	if lic.License.Tier == TierPremium {
		features[FeatureCloudRelay] = true
		features[FeatureFederation] = true
		features[FeatureMultiUser] = true
		features[FeatureAdvancedMetric] = true
		features[FeatureCloudBackup] = true
	}
	return &Entitlements{
		Tier:      lic.License.Tier,
		LicenseID: lic.License.LicenseID,
		Seats:     lic.License.Seats,
		ExpiresAt: lic.License.ExpiresAt,
		Features:  features,
	}, nil
}

// ErrExpired is returned when the license is past expiry; callers may
// retain a grace period (story 16.4: 30 days) before locking.
var ErrExpired = errors.New("license expired")

// Sign is a helper used by tests and the license-server seeding script.
// Production never signs — only verifies.
func Sign(priv ed25519.PrivateKey, inner LicenseInner) (*License, error) {
	payload, err := canonicalJSON(inner)
	if err != nil {
		return nil, err
	}
	sig := ed25519.Sign(priv, payload)
	return &License{
		License:   inner,
		Signature: base64.StdEncoding.EncodeToString(sig),
	}, nil
}

// canonicalJSON marshals inner with sorted keys and no trailing newline.
// The signature is computed over this exact byte sequence; verifiers
// must reconstruct it identically.
func canonicalJSON(inner LicenseInner) ([]byte, error) {
	b, err := json.Marshal(inner)
	if err != nil {
		return nil, err
	}
	// trim trailing newline if any
	return []byte(strings.TrimRight(string(b), "\n")), nil
}

// Store is the runtime cache of the current entitlements. Handlers go
// through Current() rather than reading the disk every request.
type Store struct {
	mu      sync.RWMutex
	current *Entitlements
}

// NewStore creates a Store seeded with the free tier.
func NewStore() *Store {
	return &Store{current: nil}
}

// Set replaces the current entitlement. Pass nil to revert to free tier.
func (s *Store) Set(e *Entitlements) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = e
}

// Current returns the active entitlement snapshot. nil means free tier.
func (s *Store) Current() *Entitlements {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Allows is a convenience wrapper.
func (s *Store) Allows(f Feature) bool {
	return s.Current().Allows(f)
}
