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

// Claim is POST /api/cluster/term-claim {from, term, sealed} (public, on every machine):
// another standby home's claim on an emergency's term, sealed from its key to this
// machine's. This machine's supervisor opens it and answers, sealed back: whether the
// term is that standby's now, or another's, or older than this machine's home's. 404 on a
// machine without a supervisor; 409 when it does not open here.
func (e *Emergency) Claim(w http.ResponseWriter, r *http.Request) {
	if e.Supervisor == nil {
		httperror.Write(w, r, httperror.NotFound("no supervisor here"))
		return
	}
	var req struct {
		From   string `json:"from"`
		Term   int64  `json:"term"`
		Sealed string `json:"sealed"`
	}
	if er := common.ReadJSON(r, &req, 16<<10); er != nil {
		httperror.Write(w, r, er)
		return
	}
	var out json.RawMessage
	if err := e.Supervisor.Call(r.Context(), "term-claim", map[string]any{"from": req.From, "term": req.Term,
		"sealed": req.Sealed}, &out); err != nil {
		httperror.Write(w, r, httperror.Conflict("https://maktaba.tv/problems/term-claim-refused",
			"the claim does not open on this machine"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(out)
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
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.tv/problems/switchover-unavailable",
			Title: "this machine cannot take over", Status: http.StatusConflict,
			Detail: "it does not run under Maktaba's supervisor"})
		return
	}
	var out json.RawMessage
	if err := e.Supervisor.Call(r.Context(), "emergency", map[string]any{"confirm": req.Confirm,
		"by": "user:" + p.UserID}, &out); err != nil {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.tv/problems/emergency-refused",
			Title: "this machine cannot take over now", Status: http.StatusConflict, Detail: err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write(out)
}
