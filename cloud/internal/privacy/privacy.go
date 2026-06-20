// Package privacy is the relay's GDPR compliance layer (Epic 30, Story
// 30.2). It provides the primitives that make the relay's anonymous
// analytics defensible by construction: server-id hashing, country-from-
// edge-header (never an IP), retention purge, deletion-on-account-delete,
// the public privacy policy, and the Article 30 processing records.
//
// The governing rule (README D1) is that the aggregate metrics tables
// carry no identifying data at all — these helpers exist for the few
// places that must reference an entity (logs) and to operate the
// retention/deletion machinery and disclosure endpoints.
package privacy

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

const (
	// RetentionDays bounds hourly-rollup lifetime (GDPR storage limitation).
	RetentionDays = 90
	// RawRetentionHours bounds per-minute raw lifetime.
	RawRetentionHours = 24
	// DefaultCountryHeader is the edge-provided country code header
	// (Cloudflare). Behind a different edge, configure accordingly.
	DefaultCountryHeader = "CF-IPCountry"
)

// HashServerID returns the first 16 hex chars of SHA-256(salt|id). The
// same construction as the on-server watch.hashIP (Epic 29 D4): stable
// for a given (salt,id), unguessable without the salt, and the raw id
// never appears in the output.
func HashServerID(salt, id string) string {
	sum := sha256.Sum256([]byte(salt + "|" + id))
	return hex.EncodeToString(sum[:])[:16]
}

// NormalizeCountry validates and upper-cases a 2-letter ISO-3166 alpha-2
// country code. Unknown/anonymised edge values — empty, Cloudflare's "XX"
// (unknown) and "T1" (Tor) — and anything not exactly two A–Z letters
// collapse to "".
func NormalizeCountry(raw string) string {
	c := strings.ToUpper(strings.TrimSpace(raw))
	if len(c) != 2 {
		return ""
	}
	if c == "XX" || c == "T1" {
		return ""
	}
	for i := 0; i < 2; i++ {
		if c[i] < 'A' || c[i] > 'Z' {
			return ""
		}
	}
	return c
}

// CountryFromRequest derives the request's country from the edge header
// and normalises it. The client IP is deliberately not read, returned,
// logged, or stored — deriving country here and discarding the address is
// the whole point (Story 30.2). header defaults to DefaultCountryHeader
// when empty.
func CountryFromRequest(r *http.Request, header string) string {
	if header == "" {
		header = DefaultCountryHeader
	}
	return NormalizeCountry(r.Header.Get(header))
}
