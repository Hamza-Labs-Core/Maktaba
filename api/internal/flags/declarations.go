// Package flags resolves the feature-flag bundle a signed-in user sees
// (Epic 16 Story 16.8) and signs it with the server's long-term Ed25519
// identity key (Story 10.18) so a client that keeps it can tell a
// tampered copy from the server's.
//
// A flag is declared here, in the binary, with its default. The bundle
// is the merge of, later winning:
//
//  1. the declared default (for a paid feature: the license's answer,
//     Entitlements.Allows, so the bundle never disagrees with the
//     server's own gates);
//  2. global overrides;
//  3. tier overrides for the active license tier;
//  4. cohort overrides for the user's beta cohorts;
//  5. user overrides.
//
// An override can turn a paid feature off, never on: the license is the
// only thing that grants one. An admin-only flag is never in a
// non-admin's bundle. An override for a key the binary does not declare
// is kept (a newer release may declare it) but left out of the bundle.
package flags

import (
	"regexp"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// Declaration is one flag the binary knows. Every declared flag is a
// boolean.
type Declaration struct {
	Key string `json:"key"`
	// Feature, when set, makes the flag the license's answer for that
	// feature; an override can only turn it off.
	Feature subscriptions.Feature `json:"feature,omitempty"`
	// Default is the value without a license feature, an override or a
	// cohort.
	Default bool `json:"default"`
	// Cohorts: membership in any of them turns the default on (a beta
	// flag a user opts into under Settings, Advanced, Experiments).
	Cohorts []string `json:"cohorts,omitempty"`
	// AdminOnly flags appear in an admin's bundle only.
	AdminOnly   bool   `json:"admin_only,omitempty"`
	Description string `json:"description"`
}

// Cohort is a beta cohort the binary declares. Admins may also place
// users in cohorts the binary does not declare (and aim cohort
// overrides at them); only a declared cohort with UserOptIn can be
// joined by the user themself.
type Cohort struct {
	Name        string `json:"name"`
	UserOptIn   bool   `json:"user_opt_in"`
	Description string `json:"description"`
}

// Flag keys the web client reads.
const (
	FlagCloudRelay          = "cloud_relay"
	FlagMultiUser           = "multi_user"
	FlagCloudMetadataBackup = "cloud_metadata_backup"
	FlagAdvancedAnalytics   = "advanced_analytics"
	FlagFederation          = "federation"
	FlagFlagsAdmin          = "flags_admin"
	FlagEarlyAccess         = "early_access"
)

// CohortEarlyAccess is the one cohort a user can join themself.
const CohortEarlyAccess = "early_access"

// Declarations is every flag this binary knows, in display order.
var Declarations = []Declaration{
	{Key: FlagCloudRelay, Feature: subscriptions.FeatureCloudRelay, Description: "Remote access through the cloud relay"},
	{Key: FlagMultiUser, Feature: subscriptions.FeatureMultiUser, Description: "More than one user account"},
	{Key: FlagCloudMetadataBackup, Feature: subscriptions.FeatureCloudBackup, Description: "Cloud backup of library metadata"},
	{Key: FlagAdvancedAnalytics, Feature: subscriptions.FeatureAdvancedMetric, Description: "Advanced watch analytics"},
	{Key: FlagFederation, Feature: subscriptions.FeatureFederation, Description: "Federation with other servers"},
	{Key: FlagFlagsAdmin, Default: true, AdminOnly: true, Description: "Feature-flag administration"},
	{Key: FlagEarlyAccess, Cohorts: []string{CohortEarlyAccess}, Description: "Features still being finished (unstable)"},
}

// Cohorts is every cohort this binary declares.
var Cohorts = []Cohort{
	{Name: CohortEarlyAccess, UserOptIn: true, Description: "Try features before they are finished. They may change or break."},
}

// Lookup returns the declaration for key.
func Lookup(key string) (Declaration, bool) {
	for _, d := range Declarations {
		if d.Key == key {
			return d, true
		}
	}
	return Declaration{}, false
}

// LookupCohort returns the declared cohort named name.
func LookupCohort(name string) (Cohort, bool) {
	for _, c := range Cohorts {
		if c.Name == name {
			return c, true
		}
	}
	return Cohort{}, false
}

var (
	keyPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
	cohortPattern = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
)

// ValidKey reports whether key is a well-formed flag key (declared or
// not).
func ValidKey(key string) bool { return keyPattern.MatchString(key) }

// ValidCohort reports whether name is a well-formed cohort name.
func ValidCohort(name string) bool { return cohortPattern.MatchString(name) }

// Scopes an override applies at, in the order they are merged.
const (
	ScopeGlobal = "global"
	ScopeTier   = "tier"
	ScopeCohort = "cohort"
	ScopeUser   = "user"
)

// ValidTier reports whether t names a license tier.
func ValidTier(t string) bool {
	switch subscriptions.Tier(t) {
	case subscriptions.TierFree, subscriptions.TierHome, subscriptions.TierPro:
		return true
	}
	return false
}
