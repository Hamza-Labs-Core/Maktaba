package cloudlink

import (
	"encoding/json"
	"net/http"
)

// StateProvider is anything that can report current link state — the
// Supervisor satisfies it. Kept as an interface so the admin handler is
// testable without a live tunnel.
type StateProvider interface {
	State() LinkState
}

// AdminHandler serves GET /admin/cloud-link on the on-prem box so an
// operator (or the first-run wizard) can see whether the tunnel is up,
// which slug was assigned, and recent errors. It is read-only and
// returns JSON.
func AdminHandler(sp StateProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		st := sp.State()
		w.Header().Set("Content-Type", "application/json")
		code := http.StatusOK
		if st.Status != "online" {
			// 503 so a health probe / load balancer treats a downed
			// tunnel as not-ready while still returning the diagnostic
			// body.
			code = http.StatusServiceUnavailable
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(st)
	}
}
