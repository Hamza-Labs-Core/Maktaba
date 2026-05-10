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

// WriteSignedURLError emits the AC-1 401 envelope for a signed-URL
// failure.
func WriteSignedURLError(w http.ResponseWriter, subType string) {
	Write(w, http.StatusUnauthorized,
		"signed-url-"+subType,
		"Signed URL invalid",
		signedURLDetail(subType))
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
