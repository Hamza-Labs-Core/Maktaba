package middleware

import (
	"encoding/json"
	"net/http"
)

// problem writes a minimal RFC 7807 problem+json response. The full
// problem-details writer lives in Epic 7's handler package; this is
// the slimmed-down version the auth middleware needs locally so it
// doesn't pull a circular dep on handlers.
func writeProblem(w http.ResponseWriter, status int, kind, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	body := map[string]any{
		"type":   kind,
		"status": status,
		"title":  http.StatusText(status),
	}
	if detail != "" {
		body["detail"] = detail
	}
	_ = json.NewEncoder(w).Encode(body)
}
