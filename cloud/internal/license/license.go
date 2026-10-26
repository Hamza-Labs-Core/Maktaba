// Package license issues the licenses a Maktaba home verifies (Epic 16
// story 16.4): Ed25519-signed JSON, signed with the cloud's entitlement
// key, whose public half release builds of the home embed.
//
// The wire format is the home's (api/internal/subscriptions): a License
// is {"license": <inner>, "signature": base64(ed25519(inner))}, where
// the signed bytes are encoding/json's output for LicenseInner in its
// field order. The two modules cannot share code (separate Go modules,
// both internal), so both pin the same golden vector in their tests:
// change a field here and there, or every license breaks.
//
// A paid cloud plan becomes a license bound to one home (server_id): the
// home's relay agent fetches it every minute and the home renews it from
// that; it lasts PlanLicenseTTL, so a home cut off from the cloud keeps
// its plan about a month (the 30-day offline grace) and then locks.
package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// Home tiers (Epic 16 story 16.2): free is single-user and LAN-only,
// home has 4 seats, pro is unlimited.
const (
	TierFree = "free"
	TierHome = "home"
	TierPro  = "pro"
)

// TierForPlan maps a cloud plan (billing.Plan*) to the home tier its
// license grants. The cloud catalog predates Epic 16's names: the entry
// paid plan ("pro", $5) is the home's Home tier, the top plan
// ("family", $10) its Pro tier. "" means no license (the free plan).
func TierForPlan(plan string) string {
	switch plan {
	case "pro":
		return TierHome
	case "family":
		return TierPro
	}
	return ""
}

// PlanLicenseTTL is how long a plan license lasts. Issued at the start
// of the UTC day, so a day's licenses are byte-identical (the home
// changes nothing on an unchanged plan).
const PlanLicenseTTL = 31 * 24 * time.Hour

// LicenseInner is the signed payload. Field order is the wire contract.
type LicenseInner struct {
	LicenseID string    `json:"license_id"`
	Tier      string    `json:"tier"`
	Seats     int       `json:"seats"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Features  []string  `json:"features,omitempty"`
	ServerID  string    `json:"server_id,omitempty"`
	Holder    string    `json:"holder,omitempty"`
}

// License is the signed wire form the home accepts.
type License struct {
	License   LicenseInner `json:"license"`
	Signature string       `json:"signature"`
}

// RevocationsInner is the signed revocation list payload.
type RevocationsInner struct {
	IssuedAt   time.Time `json:"issued_at"`
	LicenseIDs []string  `json:"license_ids"`
}

// RevocationList is the signed wire form of the revocation list.
type RevocationList struct {
	Revocations RevocationsInner `json:"revocations"`
	Signature   string           `json:"signature"`
}

// Sign signs inner with priv.
func Sign(priv ed25519.PrivateKey, inner LicenseInner) (License, error) {
	sig, err := sign(priv, inner)
	return License{License: inner, Signature: sig}, err
}

// SignRevocations signs the list with priv.
func SignRevocations(priv ed25519.PrivateKey, inner RevocationsInner) (RevocationList, error) {
	if inner.LicenseIDs == nil {
		inner.LicenseIDs = []string{}
	}
	sig, err := sign(priv, inner)
	return RevocationList{Revocations: inner, Signature: sig}, err
}

func sign(priv ed25519.PrivateKey, v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	payload := []byte(strings.TrimRight(string(b), "\n"))
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload)), nil
}

// PlanLicenseID is the stable id of a home's plan license: one per
// home, so revoking it revokes that home's plan license for good.
func PlanLicenseID(serverID string) string { return "cloud-" + serverID }

// ForPlan is the license a home's owner's plan grants, issued for the
// day of now.
func ForPlan(priv ed25519.PrivateKey, serverID, tier, holder string, now time.Time) (License, error) {
	day := now.UTC().Truncate(24 * time.Hour)
	return Sign(priv, LicenseInner{
		LicenseID: PlanLicenseID(serverID),
		Tier:      tier,
		IssuedAt:  day,
		ExpiresAt: day.Add(PlanLicenseTTL),
		ServerID:  serverID,
		Holder:    holder,
	})
}

// newID is a fresh offline license id: "lic_" and 20 random base32
// characters.
func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "lic_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}
