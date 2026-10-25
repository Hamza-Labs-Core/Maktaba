package tvchange

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

// The automatic update's TV change (Epic 35 story 35.6), for this machine's own supervisor
// only (its per-boot local token; no session, no admin):
//
//	POST /internal/updates/tv-change                       {run_id, version, sha256}
//	GET  /internal/updates/tv-change/{run_id}              the run's change and its outcome
//	POST /internal/updates/tv-change/{run_id}/withdraw     {reason} its run rolls back

// mountAutoUpdate wires the supervisor's routes.
func (h *Handler) mountAutoUpdate(r chi.Router) {
	r.Post("/internal/updates/tv-change", h.autoBegin)
	r.Get("/internal/updates/tv-change/{run}", h.autoStatus)
	r.Post("/internal/updates/tv-change/{run}/withdraw", h.autoWithdraw)
}

func supervisorOnly(w http.ResponseWriter, r *http.Request) bool {
	if !mw.FromSupervisor(r) {
		httperror.Write(w, r, httperror.Forbidden("", "this machine's supervisor only"))
		return false
	}
	return true
}

func (h *Handler) autoBegin(w http.ResponseWriter, r *http.Request) {
	if !supervisorOnly(w, r) || h.noController(w, r) {
		return
	}
	var body struct {
		RunID   string `json:"run_id"`
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
	}
	if e := common.ReadJSON(r, &body, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	st, err := h.Controller.BeginAutoUpdate(r.Context(), body.RunID, body.Version, body.SHA256)
	if err != nil {
		if conflict(w, r, err) {
			return
		}
		httperror.Write(w, r, httperror.Internal("tv change for the update: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, st)
}

func (h *Handler) autoStatus(w http.ResponseWriter, r *http.Request) {
	if !supervisorOnly(w, r) || h.noController(w, r) {
		return
	}
	st, err := h.Controller.AutoUpdate(r.Context(), chi.URLParam(r, "run"))
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tv change of the update: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, st)
}

func (h *Handler) autoWithdraw(w http.ResponseWriter, r *http.Request) {
	if !supervisorOnly(w, r) || h.noController(w, r) {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if e := common.ReadJSON(r, &body, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	st, err := h.Controller.WithdrawAutoUpdate(r.Context(), chi.URLParam(r, "run"), body.Reason)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("withdraw the update's tv change: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, st)
}

func (h *Handler) noController(w http.ResponseWriter, r *http.Request) bool {
	if h.Controller == nil {
		httperror.Write(w, r, httperror.Internal("no change controller"))
		return true
	}
	return false
}
