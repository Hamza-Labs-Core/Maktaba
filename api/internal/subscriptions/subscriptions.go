// Package subscriptions implements the entitlement layer (Epic 16):
//
//   - Tiers: free / home / pro (Story 16.2). free is the canonical
//     always-on product; home and pro are additive paid tiers with
//     distinct seat caps and feature matrices.
//   - License keys are Ed25519-signed JSON (story 16.4). The server
//     verifies the signature against a build-time public key, checks
//     expiry, and snapshots the resulting entitlement.
//   - The Entitlements struct is what handlers ask: "is feature X
//     enabled for this caller?" and "what is the seat cap?".
//
// The free tier is unconditional: every Epic 1–15 feature works without
// a key. Paid gates are *additive*:
//
//	free : 1 seat,        no relay/backup/analytics/federation
//	home : 4 seats,       relay + daily backup + basic analytics
//	pro  : unlimited seats, relay + federation + hourly backup + advanced
//
// Server-side enforcement is mandatory (Story 16.2: clients only render
// UI). Call sites use Store.Allows / Entitlements.SeatLimit.
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
	// TierFree is the always-on default. No license required. 1 seat,
	// LAN-only, no relay/backup/analytics/federation.
	TierFree Tier = "free"

	// TierHome is the entry paid tier: 4 seats, cloud relay, daily
	// backup, basic analytics. No federation (pro-only).
	TierHome Tier = "home"

	// TierPro is the top tier: unlimited seats, cloud relay,
	// federation, hourly backup, advanced analytics.
	TierPro Tier = "pro"

	// TierPremium is a DEPRECATED alias kept so pre-existing call
	// sites and tests that minted "premium" licenses still compile.
	// It maps to the pro feature set. New code must use TierHome /
	// TierPro; the verifier rejects the literal "premium" wire string.
	TierPremium = TierPro
)

// SeatsUnlimited is the SeatLimit() sentinel for tiers with no seat
// cap (pro). Callers compare against it rather than a magic 0.
const SeatsUnlimited = -1

// validTiers is the set the verifier accepts on the wire. The legacy
// "premium" string is intentionally absent — licenses must be reissued
// against the free/home/pro model (Story 16.2).
var validTiers = map[Tier]bool{
	TierFree: true,
	TierHome: true,
	TierPro:  true,
}

// tierFeatures is the per-tier additive feature matrix (Story 16.2).
// free grants nothing extra; home and pro layer on. Kept as data so a
// single table is the source of truth for every gate call site.
var tierFeatures = map[Tier][]Feature{
	TierFree: {},
	TierHome: {
		FeatureCloudRelay,
		FeatureMultiUser,
		FeatureCloudBackup,
		FeatureAdvancedMetric,
	},
	TierPro: {
		FeatureCloudRelay,
		FeatureMultiUser,
		FeatureCloudBackup,
		FeatureAdvancedMetric,
		FeatureFederation,
	},
}

// tierSeatCap is the maximum number of users (seats) a tier may
// provision. free is single-user; home is capped at 4; pro is
// unlimited. A license MAY request fewer seats than its tier cap (e.g.
// a 2-seat home plan) — SeatLimit() then honours the smaller of the
// two.
var tierSeatCap = map[Tier]int{
	TierFree: 1,
	TierHome: 4,
	TierPro:  SeatsUnlimited,
}

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
// applied first; paid-tier additions override. A nil receiver is the
// free tier (no extras).
func (e *Entitlements) Allows(f Feature) bool {
	if FreeFeatures[f] {
		return true
	}
	if e == nil {
		return false
	}
	return e.Features[f]
}

// SeatLimit reports the maximum number of provisionable users for this
// entitlement (Story 16.2 seat enforcement). A nil receiver is the
// free tier → 1 seat (single-user). pro → SeatsUnlimited. For a capped
// tier the effective limit is the smaller of the tier cap and the
// license's explicit Seats value (a license may buy fewer than the
// cap); Seats==0 on a capped tier means "use the tier cap".
func (e *Entitlements) SeatLimit() int {
	if e == nil {
		return 1
	}
	tierCap, ok := tierSeatCap[e.Tier]
	if !ok {
		// Unknown tier should never reach here (verifier rejects it),
		// but fail closed to single-user rather than unlimited.
		return 1
	}
	if tierCap == SeatsUnlimited {
		return SeatsUnlimited
	}
	if e.Seats > 0 && e.Seats < tierCap {
		return e.Seats
	}
	return tierCap
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
	if !validTiers[lic.License.Tier] {
		return nil, fmt.Errorf("unknown tier %q (must be free/home/pro)", lic.License.Tier)
	}
	if lic.License.Seats < 0 {
		return nil, errors.New("seats must be >= 0")
	}
	// Start from any explicitly-granted features, then layer the tier
	// matrix on top. The matrix is the source of truth; an explicit
	// list can only *add* (e.g. a beta capability), never subtract a
	// tier-granted feature.
	features := map[Feature]bool{}
	for _, f := range lic.License.Features {
		features[f] = true
	}
	for _, f := range tierFeatures[lic.License.Tier] {
		features[f] = true
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

// canonicalJSON marshals inner via encoding/json (Go struct-field
// declaration order, NOT lexically sorted keys) and trims a trailing
// newline. The canonicalization contract is this exact byte sequence —
// not a key-ordering rule. A future cross-language verifier must
// reproduce encoding/json's struct-field-order output byte-for-byte;
// substituting a JCS/RFC-8785 sorted-key canonicalizer here would
// silently break every signature. Sign and verify share this function.
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

	// persist, when non-nil, makes Set/Revoke durable so an applied
	// license survives a process restart (HLB-287). Nil keeps the
	// store in-memory only — the original behaviour, retained for
	// tests and the no-DB dev path.
	persist LicensePersistence
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

// SeatLimit reports the current entitlement's seat cap (Story 16.2). A
// nil/free store is single-user (1). This is the value the user-create
// gate enforces against.
func (s *Store) SeatLimit() int {
	return s.Current().SeatLimit()
}

// seatLimiter adapts a *Store to the auth handler's SeatLimiter seam
// without that package importing concrete subscriptions types beyond
// this constructor. It reads the live entitlement on every call so a
// mid-process license change (apply/revoke) takes effect immediately.
type seatLimiter struct{ s *Store }

func (l seatLimiter) SeatLimit() int { return l.s.SeatLimit() }

// NewSeatLimiter wraps the live entitlement Store as the seat-cap
// source for Epic 16 Story 16.2 enforcement on POST /api/users. A nil
// store yields a limiter that always reports the free-tier cap (1),
// which fails closed rather than panicking.
func NewSeatLimiter(s *Store) interface{ SeatLimit() int } {
	if s == nil {
		s = NewStore()
	}
	return seatLimiter{s: s}
}
