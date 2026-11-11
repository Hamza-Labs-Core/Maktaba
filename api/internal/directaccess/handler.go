package directaccess

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Routes serves the settings page's direct access (admins only):
//
//	GET  /api/direct        Status: the state, the public port, the name, the outside check
//	POST /api/direct/check  ask the cloud to check from outside now (202)
//
// The switch, the mapping and the manual port are rules.direct (PATCH /api/rules/direct).
type Routes struct {
	Service *Service
}

// Mount wires the routes.
func (h *Routes) Mount(r chi.Router) {
	r.Get("/api/direct", h.status)
	r.Post("/api/direct/check", h.check)
}

func admin(w http.ResponseWriter, r *http.Request) bool {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, (&httperror.Error{Type: httperror.TypeAuthRequired,
			Title: "sign in", Status: http.StatusUnauthorized, Detail: "sign in as an admin"}).With("code", "auth_required"))
		return false
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return false
	}
	return true
}

func (h *Routes) status(w http.ResponseWriter, r *http.Request) {
	if !admin(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(h.Service.Status())
}

func (h *Routes) check(w http.ResponseWriter, r *http.Request) {
	if !admin(w, r) {
		return
	}
	if err := h.Service.CheckNow(r.Context()); err != nil {
		httperror.Write(w, r, (&httperror.Error{Type: httperror.Type("direct-offline"),
			Title: "the cloud can't be reached", Status: http.StatusServiceUnavailable,
			Detail: "the relay agent has no tunnel to a cloud with direct access"}).With("code", "direct_offline"))
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
