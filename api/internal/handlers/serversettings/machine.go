package serversettings

// This machine's hardware and tier (Epic 59 story 59.04), for the web's Settings → This
// machine:
//
//	GET /api/server/machine        what the server found: memory (a container's limit, the
//	                               ZFS cache counted as available), CPU and its features, the
//	                               GPUs, the data disk, the platform; the tier, why it was
//	                               chosen and what it sets ({available: false} without a
//	                               supervisor)
//	PUT /api/server/machine        {"tier": "light"|"standard"|"strong"|"auto"}: the tier
//	                               for the next start (auto: detected from the memory)
//
// They proxy the supervisor's /config/machine. Admins only (403 before the supervisor is
// called); a pick is audited (category 'server').

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// ActionTierChanged is the audit action of an admin's tier pick.
const ActionTierChanged = "tier-changed"

func (h *Handler) mountMachine(r chi.Router) {
	r.Get("/api/server/machine", h.admin(h.storageGet("machine")))
	r.Put("/api/server/machine", h.admin(h.machinePut))
}

func (h *Handler) machinePut(w http.ResponseWriter, r *http.Request) {
	if h.Supervisor == nil {
		httperror.Write(w, r, httperror.Conflict("no-supervisor", errNoSupervisor.Error()))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, common.MaxBodyBytes))
	if err != nil {
		httperror.Write(w, r, httperror.BadRequest("body too large"))
		return
	}
	var in struct {
		Tier string `json:"tier"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		httperror.Write(w, r, httperror.BadRequest("the tier is not a JSON object"))
		return
	}
	out, _ := json.Marshal(map[string]string{"tier": in.Tier})
	code, resp, err := h.storageSupervisor().call(r.Context(), http.MethodPut, "machine", out)
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway("the server's supervisor did not answer").Wrap(err))
		return
	}
	if code == http.StatusOK {
		h.audit(r, ActionTierChanged, map[string]any{"tier": in.Tier})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(resp)
}
