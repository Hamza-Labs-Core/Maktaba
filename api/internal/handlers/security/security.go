// Package security wires the public security endpoints:
//
//	GET /.well-known/security.txt — coordinated disclosure metadata
//	GET /api/system/sbom          — current build's SBOM (admin only)
package security

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/security"
)

// Handler bundles deps.
type Handler struct {
	Policy security.DisclosurePolicy
	SBOM   *security.SBOM
}

// Mount attaches the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/.well-known/security.txt", h.SecurityTxt)
	r.Get("/api/system/sbom", h.GetSBOM)
}

// SecurityTxt serves the RFC 9116 document. Public, no auth.
func (h *Handler) SecurityTxt(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(h.Policy.SecurityTxt()))
}

// GetSBOM returns the parsed SBOM. Admin-only because it leaks the
// exact dependency tree, which is useful intel for attackers.
func (h *Handler) GetSBOM(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin required"))
		return
	}
	if h.SBOM == nil {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"components": []any{}})
		return
	}
	common.WriteJSON(w, r, http.StatusOK, h.SBOM)
}
