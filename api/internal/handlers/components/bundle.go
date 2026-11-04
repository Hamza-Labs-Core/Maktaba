package components

import (
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Story 51.17: changing bundle after setup (admin-only, behind adminOnly as every route
// of the package).
//
//	GET  /api/components/bundles   the bundles and the one the home is based on
//	POST /api/components/bundle    {bundle, dry_run?, remove_unused?, generation?}: the
//	                               difference; without dry_run it is applied in one batch

type bundleBody struct {
	Bundle       string `json:"bundle"`
	DryRun       bool   `json:"dry_run,omitempty"`
	RemoveUnused bool   `json:"remove_unused,omitempty"`
	Generation   *int64 `json:"generation,omitempty"`
}

func (h *Handler) bundles(w http.ResponseWriter, r *http.Request) {
	v, err := h.Service.Bundles(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, v)
}

func (h *Handler) bundle(w http.ResponseWriter, r *http.Request) {
	var b bundleBody
	if e := common.ReadJSON(r, &b, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	d, err := h.Service.Bundle(r.Context(), b.Bundle, b.RemoveUnused, b.DryRun, b.Generation, h.actor(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, d)
}
