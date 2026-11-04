package components

import (
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
)

// resetSettings is POST /api/components/{id}/settings/reset (story 51.13): the component's
// rules sections back to the defaults (both keys of each deleted, one audit row per
// section with the old value). The web and the CLI ask first. Answers what was deleted.
func (h *Handler) resetSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := h.known(w, r)
	if !ok {
		return
	}
	res, err := h.Service.ResetSettings(r.Context(), id, h.actor(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, res)
}
