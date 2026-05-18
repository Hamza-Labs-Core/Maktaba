// Package httpx contains shared HTTP helpers for the Streaming
// service: the RFC 7807 problem+json writer (Story 8.1 §3) and the
// signed-URL error sub-types (Story 8.1 AC-1).
package httpx

import (
	"encoding/json"
	"net/http"
)

// Problem is the canonical RFC 7807 envelope. We pin our own type URI
// scheme under maktaba.invalid/problems/ because we don't expose a
// public dereferenceable docs URL.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Write emits a problem+json body. Always sets nosniff so browsers
// don't try to render an error response as HTML.
func Write(w http.ResponseWriter, status int, problemType, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{
		Type:   "https://maktaba.invalid/problems/" + problemType,
		Title:  title,
		Status: status,
		Detail: detail,
	})
}

// SignedURL error sub-types (Story 8.1 AC-1). Centralized so handlers
// and tests agree on the exact strings.
const (
	SignedURLMissing      = "missing"
	SignedURLExpired      = "expired"
	SignedURLWrongAud     = "wrong-aud"
	SignedURLWrongSub     = "wrong-sub"
	SignedURLWrongLib     = "wrong-lib"
	SignedURLBadSignature = "bad-signature"
)

// WriteSignedURLError emits the signed-URL failure envelope. The
// status is sub-type-precise (Story 23.2 AC-3):
//
//   - missing / bad-signature → 401 Unauthorized: no usable
//     credential was presented (or it isn't trustworthy at all), so
//     the correct answer is "authenticate".
//   - expired → 403 Forbidden: a well-formed, signature-valid token
//     was presented but is past its exp. AC-3 explicitly requires a
//     "clear 403 (not 401)" so a player distinguishes "re-mint the
//     URL" from "log in again".
//
// Other refused-but-authenticated sub-types (wrong-aud / wrong-sub /
// wrong-lib) keep the original 401 — they are out of this change's
// scope (Epic 10 owns the entitlement-claim minting side) and are
// left untouched to avoid behavioural drift in that lane.
func WriteSignedURLError(w http.ResponseWriter, subType string) {
	Write(w, signedURLStatus(subType),
		"signed-url-"+subType,
		"Signed URL invalid",
		signedURLDetail(subType))
}

// signedURLStatus maps a signed-URL sub-type to its HTTP status.
// Expired is the only sub-type promoted to 403 per Story 23.2 AC-3.
func signedURLStatus(subType string) int {
	if subType == SignedURLExpired {
		return http.StatusForbidden
	}
	return http.StatusUnauthorized
}

func signedURLDetail(s string) string {
	switch s {
	case SignedURLMissing:
		return "the request did not carry a JWT (no ?sig= and no Authorization header)"
	case SignedURLExpired:
		return "the JWT is past its exp claim (with leeway applied)"
	case SignedURLWrongAud:
		return "the JWT's aud does not match this endpoint's required audience"
	case SignedURLWrongSub:
		return "the JWT's sub does not match the URL's subject (session, video, or artifact)"
	case SignedURLWrongLib:
		return "the JWT's lib[] claim does not include the resource's library"
	case SignedURLBadSignature:
		return "the JWT signature did not verify against any key in the JWKS"
	default:
		return "the JWT is not acceptable for this request"
	}
}
