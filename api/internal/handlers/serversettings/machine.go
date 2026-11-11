package serversettings

// This machine's hardware and tier (Epic 59 story 59.04), for the web's Settings → This
// machine:
//
//	GET /api/server/machine        what the server found: memory (a container's limit, the
//	                               ZFS cache counted as available), CPU and its features, the
//	                               GPUs, the data disk, the platform; the tier, why it was
//	                               chosen and what it sets ({available: false} without a
//	                               supervisor)
//	PUT /api/server/machine        each field optional: {"tier": "light"|"standard"|
//	                               "strong"|"auto"}: the tier for the next start (auto:
//	                               detected from the memory); {"quiet_hours": "01:00-06:00"
//	                               |""} or {"quiet_default": true} (59.02's quiet hours);
//	                               {"data_disk": "ssd"|"rotational"|"auto"} (59.02)
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

// ActionTierChanged is the audit action of an admin's tier pick; ActionMachineChanged of
// a change to this machine's quiet hours or data disk (Epic 59 story 59.02).
const (
	ActionTierChanged    = "tier-changed"
	ActionMachineChanged = "machine-changed"
)

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
	// only the fields the supervisor takes go on, and only those sent: a quiet hours change
	// carries no tier (an empty one would be refused as no tier at all)
	var in struct {
		Tier         *string         `json:"tier,omitempty"`
		QuietHours   json.RawMessage `json:"quiet_hours,omitempty"`
		QuietDefault bool            `json:"quiet_default,omitempty"`
		DataDisk     *string         `json:"data_disk,omitempty"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		httperror.Write(w, r, httperror.BadRequest("the change is not a JSON object"))
		return
	}
	if string(in.QuietHours) == "null" {
		in.QuietHours = nil
	}
	out, _ := json.Marshal(in)
	code, resp, err := h.storageSupervisor().call(r.Context(), http.MethodPut, "machine", out)
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway("the server's supervisor did not answer").Wrap(err))
		return
	}
	if code == http.StatusOK {
		if in.Tier != nil {
			h.audit(r, ActionTierChanged, map[string]any{"tier": *in.Tier})
		}
		if in.QuietHours != nil || in.QuietDefault || in.DataDisk != nil {
			detail := map[string]any{}
			if in.QuietHours != nil {
				var q string
				_ = json.Unmarshal(in.QuietHours, &q)
				detail["quiet_hours"] = q
			}
			if in.QuietDefault {
				detail["quiet_default"] = true
			}
			if in.DataDisk != nil {
				detail["data_disk"] = *in.DataDisk
			}
			h.audit(r, ActionMachineChanged, detail)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(resp)
}
