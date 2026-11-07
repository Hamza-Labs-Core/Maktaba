// Package plugins holds the HTTP routes of Epic 54 on the API's TCP listener.
//
//	POST   /internal/plugins/invocations       {job_id} → a token for a plugin job (54.04 §2)
//	DELETE /internal/plugins/invocations/{id}  the job ended
//	POST   /internal/plugins/breaker           {plugin_id, reason}: its breaker tripped (54.09 §2)
//
// Only this machine's own processes mint: the request carries the supervisor's per-boot
// local token (middleware.FromSupervisor), which the pipeline worker has as a child of the
// supervisor. A signed-in user, an admin or a cloud-linked sign-in gets 403; no
// credential at all gets 401. The body's grants, if any, are ignored: they come from
// plugin_grants only.
package plugins

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
	pl "github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
)

// Invocations serves the mint and end routes, and the breaker's.
type Invocations struct {
	Tokens *pl.Tokens
	// Trip holds a plugin its dispatcher's circuit breaker tripped (story 54.09 §2:
	// POST /internal/plugins/breaker {plugin_id, reason}); nil: 503.
	Trip func(r *http.Request, pluginID, reason string) (bool, error)
}

func machineOnly(w http.ResponseWriter, r *http.Request) bool {
	if middleware.FromSupervisor(r) {
		return true
	}
	status := http.StatusUnauthorized
	if principal.FromContext(r.Context()) != nil {
		status = http.StatusForbidden
	}
	writeJSON(w, status, map[string]string{"code": "machine_only"})
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Mount adds the routes to r.
func (h *Invocations) Mount(r chi.Router) {
	r.Post("/internal/plugins/invocations", h.mint)
	r.Delete("/internal/plugins/invocations/{id}", h.end)
	r.Post("/internal/plugins/breaker", h.breaker)
}

// breaker: only the API writes desired state (Epic 51 H13), so the pipeline's dispatcher
// asks it to hold a plugin whose breaker tripped. {held}: false when it was held already.
func (h *Invocations) breaker(w http.ResponseWriter, r *http.Request) {
	if !machineOnly(w, r) {
		return
	}
	var in struct {
		PluginID string `json:"plugin_id"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil || in.PluginID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "plugin_invalid"})
		return
	}
	if h.Trip == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "plugins_unavailable"})
		return
	}
	held, err := h.Trip(r, in.PluginID, in.Reason)
	var p *pl.Problem
	switch {
	case errors.As(err, &p):
		writeJSON(w, p.Status, map[string]string{"code": p.Code})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal"})
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"held": held})
	}
}

func (h *Invocations) mint(w http.ResponseWriter, r *http.Request) {
	if !machineOnly(w, r) {
		return
	}
	var in struct {
		JobID int64 `json:"job_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil || in.JobID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "plugin_invalid"})
		return
	}
	m, err := h.Tokens.Mint(r.Context(), in.JobID)
	var ref *pl.Refusal
	switch {
	case errors.As(err, &ref):
		writeJSON(w, ref.Status, map[string]string{"code": ref.Code})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal"})
	default:
		writeJSON(w, http.StatusOK, m)
	}
}

func (h *Invocations) end(w http.ResponseWriter, r *http.Request) {
	if !machineOnly(w, r) {
		return
	}
	ended, refused := h.Tokens.EndCounting(r.Context(), chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]any{"ended": ended, "refusals": refused})
}
