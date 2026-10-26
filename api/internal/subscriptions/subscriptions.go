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

// AllFeatures lists every gated feature, in a stable order.
var AllFeatures = []Feature{FeatureCloudRelay, FeatureMultiUser, FeatureCloudBackup, FeatureAdvancedMetric, FeatureFederation}

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
	IssuedAt  time.Time
	ExpiresAt time.Time
	Features  map[Feature]bool
	ServerID  string
	Holder    string
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
	// ServerID binds a license to one home claimed in Maktaba Cloud
	// (the cloud issues these from the owner's plan and the home
	// refreshes them daily). Empty: an offline license, valid on any
	// home until it expires or is revoked.
	ServerID string `json:"server_id,omitempty"`
	// Holder names who the license is for (shown in Settings).
	Holder string `json:"holder,omitempty"`
}

// Verifier checks license signatures against a public key. Production
// loads the key from a build-time embedded constant; tests construct
// their own.
type Verifier struct {
	PublicKey ed25519.PublicKey
}

// Verify returns the entitlement implied by lic, or an error: the
// signature, the tier, the seats, and expiry against now.
func (v *Verifier) Verify(lic *License, now time.Time) (*Entitlements, error) {
	ent, err := v.VerifySignature(lic)
	if err != nil {
		return nil, err
	}
	if !now.Before(lic.License.ExpiresAt) {
		return nil, ErrExpired
	}
	return ent, nil
}

// VerifySignature checks everything but expiry: the Store keeps an
// expired license it loaded (to say "expired" in Settings) while
// granting nothing from it.
func (v *Verifier) VerifySignature(lic *License) (*Entitlements, error) {
	if lic == nil {
		return nil, errors.New("nil license")
	}
	sig, err := base64.StdEncoding.DecodeString(lic.Signature)
	if err != nil {
		return nil, ErrBadSignature
	}
	payload, err := canonicalJSON(lic.License)
	if err != nil {
		return nil, err
	}
	if v == nil || v.PublicKey == nil {
		return nil, errors.New("verifier has no public key")
	}
	if !ed25519.Verify(v.PublicKey, payload, sig) {
		return nil, ErrBadSignature
	}
	if !validTiers[lic.License.Tier] {
		return nil, fmt.Errorf("unknown tier %q (must be free/home/pro)", lic.License.Tier)
	}
	if lic.License.Seats < 0 {
		return nil, errors.New("seats must be >= 0")
	}
	if lic.License.LicenseID == "" {
		return nil, errors.New("license has no id")
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
		IssuedAt:  lic.License.IssuedAt,
		ExpiresAt: lic.License.ExpiresAt,
		Features:  features,
		ServerID:  lic.License.ServerID,
		Holder:    lic.License.Holder,
	}, nil
}

// ErrBadSignature: the license was altered, or another key signed it.
var ErrBadSignature = errors.New("signature verification failed")

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
func canonicalJSON(inner any) ([]byte, error) {
	b, err := json.Marshal(inner)
	if err != nil {
		return nil, err
	}
	// trim trailing newline if any
	return []byte(strings.TrimRight(string(b), "\n")), nil
}
