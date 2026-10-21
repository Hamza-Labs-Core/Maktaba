package switchover

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Mount wires the control centre's routes (admins only):
//
//	GET  /api/cluster/switchover  the term, the switch under way or the last ones, the
//	                              machines that could take over and why not
//	POST /api/cluster/switchover  {to_node}: start a planned switch
func (o *Orchestrator) Mount(r chi.Router) {
	r.Get("/api/cluster/switchover", o.getStatus)
	r.Post("/api/cluster/switchover", o.postStart)
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
	Term       *Term       `json:"term"`
	Current    *Progress   `json:"current"`
	Recent     []Progress  `json:"recent"`
	Candidates []Candidate `json:"candidates"`
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
	common.WriteJSON(w, r, http.StatusOK, s)
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
