package switchover

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Mount wires the control centre's routes (admins only):
//
//	GET  /api/cluster/switchover          the term, the switch under way or the last ones,
//	                                      the machines that could take over and why not,
//	                                      the standby homes, the old homes' reports
//	POST /api/cluster/switchover          {to_node}: start a planned switch
//	POST /api/cluster/switchover/{id}/cancel  {force, confirm}: call a switch under way off
//	PUT  /api/cluster/switchover/standby  {node_id, standby}: mark a standby home
//
// and the old homes' reports (mountKept).
func (o *Orchestrator) Mount(r chi.Router) {
	r.Get("/api/cluster/switchover", o.getStatus)
	r.Post("/api/cluster/switchover", o.postStart)
	r.Post("/api/cluster/switchover/{id}/cancel", o.postCancel)
	r.Put("/api/cluster/switchover/standby", o.putStandby)
	r.Post("/api/cluster/switchover/{id}/checklist", o.postChecklist)
	o.mountKept(r)
}

// postCancel is POST /api/cluster/switchover/{id}/cancel {force, confirm} (Cancel): 200
// {state}; 409 when the machine taking over is (becoming) the home, or does not answer
// (then force with its name typed in confirm calls it off all the same; its name comes
// back in "machine").
func (o *Orchestrator) postCancel(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	var req struct {
		Force   bool   `json:"force"`
		Confirm string `json:"confirm"`
	}
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	id := chi.URLParam(r, "id")
	state, err := o.Cancel(r.Context(), id, req.Force, req.Confirm)
	var name string
	if p := o.Progress(id); p != nil {
		name = p.ToName
	}
	switch {
	case errors.Is(err, ErrNoSwitch):
		httperror.Write(w, r, httperror.NotFound("switch under way"))
	case errors.Is(err, ErrTakingOver):
		httperror.Write(w, r, httperror.Conflict("https://maktaba.dev/problems/switchover-taking-over", err.Error()).
			With("machine", name))
	case errors.Is(err, ErrNoAnswer):
		httperror.Write(w, r, httperror.Conflict("https://maktaba.dev/problems/switchover-no-answer", err.Error()).
			With("machine", name))
	case errors.Is(err, ErrConfirmName):
		httperror.Write(w, r, httperror.BadRequest(err.Error()).With("machine", name))
	case err != nil:
		httperror.Write(w, r, httperror.Internal("switchover"))
	default:
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"state": state})
	}
}

// postChecklist is POST /api/cluster/switchover/{id}/checklist {item, done}: a person
// did (or did not) what an outside service needed after a switch.
func (o *Orchestrator) postChecklist(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	var req struct {
		Item string `json:"item"`
		Done bool   `json:"done"`
	}
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	err := o.MarkDone(r.Context(), chi.URLParam(r, "id"), req.Item, req.Done)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("checklist item"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("switchover"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"done": req.Done})
}

func admin(w http.ResponseWriter, r *http.Request) (*principal.Principal, bool) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return nil, false
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return nil, false
	}
	return p, true
}

// Status is GET /api/cluster/switchover.
type Status struct {
	Term       *Term          `json:"term"`
	Current    *Progress      `json:"current"`
	Recent     []Progress     `json:"recent"`
	Candidates []Candidate    `json:"candidates"`
	Standbys   []StandbyState `json:"standbys"`
	Reports    []KeptReport   `json:"reports"`
}

func (o *Orchestrator) getStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	ctx := r.Context()
	var s Status
	if t, err := o.CurrentTerm(ctx); err == nil {
		s.Term = &t
	}
	s.Current = o.active()
	if rec, err := o.Recorded(ctx, 5); err == nil {
		// the checklist of the switch that made this machine the home, checked again
		if len(rec) > 0 && rec[0].State == "done" {
			o.recheck(ctx, &rec[0])
		}
		s.Recent = rec
		if s.Current == nil && len(rec) > 0 && rec[0].State != "done" && rec[0].State != "failed" &&
			rec[0].State != "cancelled" {
			cur := rec[0]
			s.Current = &cur
		}
	}
	if s.Current != nil {
		// the steps are this home's: the database has the state
		if p := o.Progress(s.Current.ID); p != nil {
			s.Current = p
		}
	}
	cs, err := o.Candidates(ctx)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("switchover"))
		return
	}
	s.Candidates = cs
	if s.Standbys, err = o.Standbys(ctx); err != nil {
		httperror.Write(w, r, httperror.Internal("switchover"))
		return
	}
	if s.Reports, err = o.Reports(r); err != nil {
		httperror.Write(w, r, httperror.Internal("switchover"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, s)
}

func (o *Orchestrator) putStandby(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	var req struct {
		NodeID  string `json:"node_id"`
		Standby bool   `json:"standby"`
	}
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	err := o.SetStandby(r.Context(), req.NodeID, req.Standby)
	if errors.Is(err, ErrNotStandby) {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.dev/problems/not-standby",
			Title: "that machine cannot be a standby home", Status: http.StatusConflict, Detail: err.Error()})
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("switchover"))
		return
	}
	ss, err := o.Standbys(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("switchover"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"standbys": ss})
}

func (o *Orchestrator) postStart(w http.ResponseWriter, r *http.Request) {
	p, ok := admin(w, r)
	if !ok {
		return
	}
	var req struct {
		ToNode string `json:"to_node"`
	}
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if o.Supervisor == nil || o.Hub == nil {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.dev/problems/switchover-unavailable",
			Title: "this home cannot be switched", Status: http.StatusConflict,
			Detail: "it does not run under Maktaba's supervisor with its own Postgres"})
		return
	}
	id, err := o.Start(r.Context(), req.ToNode, "user:"+p.UserID)
	if errors.Is(err, ErrNotReady) {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.dev/problems/switchover-not-ready",
			Title: "that machine cannot take over now", Status: http.StatusConflict, Detail: err.Error()})
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("switchover"))
		return
	}
	common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"id": id})
}
