package keys

import (
	"net/http"
)

// JWKSHandler is the http.Handler that publishes the trust set at
// `/api/.well-known/jwks.json` (Story 10.6 AC-3).
//
// Cache-Control is set to `public, max-age=300` per AC-3 so a
// reasonable Streaming/CDN cache holds it for 5 minutes between
// rotations; the LISTEN-based push notification in plan-10-06 is the
// reason a longer TTL is acceptable.
type JWKSHandler struct {
	Set *Set
}

func (h *JWKSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := h.Set.JWKS()
	if err != nil {
		http.Error(w, "jwks unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/jwk-set+json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}
