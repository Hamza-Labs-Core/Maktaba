package components

// The per-machine routes (story 51.12), admin-only as every route of the package:
//
//	GET  /api/components?machine=<node_id>                  every component with that machine's wish, override and status
//	PUT  /api/components/{id}/machines/{node_id}            {want: enabled|installed|absent|null, generation?, with_conflicts?}; null follows the home
//	POST /api/components/{id}/machines/{node_id}/stop       held on that machine until start (a body with until is 400 hold_has_no_until)
//	POST /api/components/{id}/machines/{node_id}/start      clear that machine's hold
//
// POST /api/components/preview and /apply take changes with "machine": "<node_id>".
// Each answers the component as that machine now has it.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

func (h *Handler) mountMachines(r chi.Router) {
	r.Put("/api/components/{id}/machines/{node}", h.putMachine)
	r.Post("/api/components/{id}/machines/{node}/stop", h.stopMachine)
	r.Post("/api/components/{id}/machines/{node}/start", h.startMachine)
}

func (h *Handler) listMachine(w http.ResponseWriter, r *http.Request, node string) {
	l, err := h.Service.ListMachine(r.Context(), node)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, l)
}

// oneMachine applies a single change on node and answers the component as it now is
// there.
func (h *Handler) oneMachine(w http.ResponseWriter, r *http.Request, id, node string, req svc.Request) {
	for i := range req.Changes {
		req.Changes[i].Machine = node
	}
	if _, err := h.Service.Apply(r.Context(), req, h.actor(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	l, err := h.Service.ListMachine(r.Context(), node)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	for _, c := range l.Components {
		if c.ID == id {
			common.WriteJSON(w, r, http.StatusOK, c)
			return
		}
	}
	h.fail(w, r, svc.UnknownRefusal(id))
}

type putMachineBody struct {
	Want          json.RawMessage `json:"want"`
	Generation    *int64          `json:"generation,omitempty"`
	WithConflicts bool            `json:"with_conflicts,omitempty"`
}

func (h *Handler) putMachine(w http.ResponseWriter, r *http.Request) {
	id, ok := h.known(w, r)
	if !ok {
		return
	}
	var b putMachineBody
	if e := common.ReadJSON(r, &b, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	want := ""
	switch raw := strings.TrimSpace(string(b.Want)); raw {
	case "":
		// no want at all: refused by the service (bad_want)
	case "null":
		want = svc.WantFollow
	default:
		if err := json.Unmarshal(b.Want, &want); err != nil || want == "" {
			want = "?" + raw // refused as bad_want, with what was sent
		}
	}
	h.oneMachine(w, r, id, chi.URLParam(r, "node"), svc.Request{WithConflicts: b.WithConflicts,
		Changes: []svc.Change{{ID: id, Want: want, Generation: b.Generation}}})
}

func (h *Handler) stopMachine(w http.ResponseWriter, r *http.Request) {
	id, ok := h.known(w, r)
	if !ok {
		return
	}
	if !h.noUntil(w, r) {
		return
	}
	held := true
	h.oneMachine(w, r, id, chi.URLParam(r, "node"), svc.Request{Changes: []svc.Change{{ID: id, Hold: &held}}})
}

func (h *Handler) startMachine(w http.ResponseWriter, r *http.Request) {
	id, ok := h.known(w, r)
	if !ok {
		return
	}
	held := false
	h.oneMachine(w, r, id, chi.URLParam(r, "node"), svc.Request{Changes: []svc.Change{{ID: id, Hold: &held}}})
}

// noUntil refuses a stop body that asks for an end time: a hold lasts until start.
func (h *Handler) noUntil(w http.ResponseWriter, r *http.Request) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
	if err != nil {
		httperror.Write(w, r, httperror.BadRequest("body too large"))
		return false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		httperror.Write(w, r, httperror.BadRequest("stop takes no body"))
		return false
	}
	if _, has := body["until"]; has {
		httperror.Write(w, r, problem(http.StatusBadRequest, svc.CodeNoUntil, "a hold has no end time",
			"a stopped component stays stopped until it is started"))
		return false
	}
	return true
}

// ClusterHook is the components' side of the Machines routes (POST
// /api/cluster/nodes/{id}/approve with components, PUT /api/cluster/nodes/{id} with
// stages): it implements the cluster handler's MachineComponents. A refusal comes back
// as its problem (an *httperror.Error).
type ClusterHook struct{ H *Handler }

// Approve writes the machine's chosen components as it is approved.
func (c ClusterHook) Approve(r *http.Request, nodeID string, keep []string) error {
	return problemOf(c.H.Service.WorkerChoice(r.Context(), nodeID, keep, c.H.actor(r)))
}

// StagesSwitched keeps the machine's component switches in step with its stage table.
func (c ClusterHook) StagesSwitched(r *http.Request, nodeID string, stages map[string]*bool) error {
	return problemOf(c.H.Service.StagesSwitched(r.Context(), nodeID, stages, c.H.actor(r)))
}

func problemOf(err error) error {
	if ref, ok := svc.AsRefusal(err); ok {
		return refusalProblem(ref)
	}
	return err
}
