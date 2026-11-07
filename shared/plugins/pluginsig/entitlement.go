package pluginsig

import (
	"encoding/json"
	"time"
)

// What a home checks of an entitlement beyond its signature (Epic 54, story 54.19 §6): that
// it names this home and this home's store key, and that it is valid now, with Epic 16's
// grace. VerifyEntitlement checks the envelope (an entitlement key of store-keys.json);
// CheckEntitlement adds the rest. Nothing in an entitlement names a payment provider.

// StepEntitlement is the step a refusal of the entitlement's own content names.
const StepEntitlement = "entitlement"

// Refusals of an entitlement's content.
const (
	CodeEntitlementOtherHome = "entitlement_other_home"
	CodeEntitlementExpired   = "entitlement_expired"
)

// EntitlementGrace is the clock skew allowed around an entitlement's validity: Epic 16's
// SkewGrace, the same number as the home's licences (72 hours).
const EntitlementGrace = 72 * time.Hour

// Entitlement is a checked entitlement's content (version 1).
type Entitlement struct {
	V          int       `json:"v"`
	HomeID     string    `json:"home_id"`
	HomeKeyFP  string    `json:"home_key_fp"`
	PluginID   string    `json:"plugin_id"`
	Product    string    `json:"product"`
	PurchaseID string    `json:"purchase_id"`
	ValidFrom  time.Time `json:"-"`
	ValidUntil time.Time `json:"-"`
	IssuedAt   time.Time `json:"-"`
	// Raw is the envelope as the store signed it.
	Raw json.RawMessage `json:"-"`
}

type entitlementWire struct {
	V          int    `json:"v"`
	HomeID     string `json:"home_id"`
	HomeKeyFP  string `json:"home_key_fp"`
	PluginID   string `json:"plugin_id"`
	Product    string `json:"product"`
	PurchaseID string `json:"purchase_id"`
	ValidFrom  string `json:"valid_from"`
	ValidUntil string `json:"valid_until"`
	IssuedAt   string `json:"issued_at"`
}

// CheckEntitlement checks a signed entitlement for this home: the envelope (VerifyEntitlement),
// version 1 with every field, this home's id and store key fingerprint, and now within its
// validity widened by EntitlementGrace on both sides.
func CheckEntitlement(raw []byte, keys *StoreKeys, st State, clock time.Time, homeID, homeKeyFP string) (*Entitlement, error) {
	signed, err := VerifyEntitlement(raw, keys, st, clock)
	if err != nil {
		return nil, err
	}
	var w entitlementWire
	if err := strictUnmarshal(signed, &w); err != nil || w.V != 1 || w.PluginID == "" || w.PurchaseID == "" {
		return nil, fail(CodeEntitlementInvalid, StepEntitlement, "not a version 1 entitlement")
	}
	from, ferr := parseTime("valid_from", w.ValidFrom)
	until, uerr := parseTime("valid_until", w.ValidUntil)
	issued, ierr := parseTime("issued_at", w.IssuedAt)
	if ferr != nil || uerr != nil || ierr != nil {
		return nil, fail(CodeEntitlementInvalid, StepEntitlement, "a time is not RFC 3339")
	}
	if w.HomeID != homeID || w.HomeKeyFP != homeKeyFP {
		return nil, fail(CodeEntitlementOtherHome, StepEntitlement, "for another home or another home key")
	}
	now := st.Now(clock)
	if now.Before(from.Add(-EntitlementGrace)) || now.After(until.Add(EntitlementGrace)) {
		return nil, fail(CodeEntitlementExpired, StepEntitlement, "valid %s to %s", w.ValidFrom, w.ValidUntil)
	}
	return &Entitlement{V: w.V, HomeID: w.HomeID, HomeKeyFP: w.HomeKeyFP, PluginID: w.PluginID, Product: w.Product,
		PurchaseID: w.PurchaseID, ValidFrom: from, ValidUntil: until, IssuedAt: issued, Raw: raw}, nil
}
