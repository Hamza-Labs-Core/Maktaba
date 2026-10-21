package switchover

import (
	"encoding/json"
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Emergency is a standby home's own (specs/epics/37-node-replica, "Emergency
// promotion"): with its home gone for good, its admin makes it the home from here. Its
// supervisor checks, and does it: it holds the home's kit, the home does not answer (two
// homes never write), its copy follows; the home's name typed to confirm.
//
//	GET  /api/cluster/emergency  whether this machine can take over now, and why not
//	POST /api/cluster/emergency  {confirm}: take over
type Emergency struct {
	Supervisor Supervisor
}

// Status is GET /api/cluster/emergency.
func (e *Emergency) Status(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	if e.Supervisor == nil {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"standby": false, "ready": false,
			"reasons": []string{"no_supervisor"}})
		return
	}
	var st json.RawMessage
	if err := e.Supervisor.Call(r.Context(), "emergency-status", map[string]any{}, &st); err != nil {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"standby": false, "ready": false,
			"reasons": []string{"no_copy"}, "detail": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(st)
}

// Start is POST /api/cluster/emergency.
func (e *Emergency) Start(w http.ResponseWriter, r *http.Request) {
	p, ok := admin(w, r)
	if !ok {
		return
	}
	var req struct {
		Confirm string `json:"confirm"`
	}
	if er := common.ReadJSON(r, &req, 4<<10); er != nil {
		httperror.Write(w, r, er)
		return
	}
	if e.Supervisor == nil {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.dev/problems/switchover-unavailable",
			Title: "this machine cannot take over", Status: http.StatusConflict,
			Detail: "it does not run under Maktaba's supervisor"})
		return
	}
	var out json.RawMessage
	if err := e.Supervisor.Call(r.Context(), "emergency", map[string]any{"confirm": req.Confirm,
		"by": "user:" + p.UserID}, &out); err != nil {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.dev/problems/emergency-refused",
			Title: "this machine cannot take over now", Status: http.StatusConflict, Detail: err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write(out)
}
