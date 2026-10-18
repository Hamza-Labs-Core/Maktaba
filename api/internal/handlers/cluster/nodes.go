package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// NodeWorker is one of a machine's workers.
type NodeWorker struct {
	ID         string          `json:"id"`
	Host       string          `json:"host"`
	Online     bool            `json:"online"`
	LastSeenAt time.Time       `json:"last_seen_at"`
	Stages     []string        `json:"stages"`
	StagesOff  json.RawMessage `json:"stages_off"`
}

// Node is a machine as the admin sees it: the row without keys or
// secrets, its workers and its stage switches.
type Node struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	State          string          `json:"state"`
	Expired        bool            `json:"expired"`
	OS             *string         `json:"os"`
	Arch           *string         `json:"arch"`
	Versions       json.RawMessage `json:"versions"`
	Capabilities   json.RawMessage `json:"capabilities"`
	Storage        json.RawMessage `json:"storage"`
	PathMap        json.RawMessage `json:"path_map"`
	PathMapSource  *string         `json:"path_map_source"`
	Drain          bool            `json:"drain"`
	CodeAttempts   int             `json:"code_attempts"`
	AttemptsLeft   int             `json:"attempts_left"`
	ExpiresAt      *time.Time      `json:"expires_at"`
	DBRole         *string         `json:"db_role"`
	RemoteAddr     *string         `json:"remote_addr"`
	CreatedAt      time.Time       `json:"created_at"`
	ApprovedAt     *time.Time      `json:"approved_at"`
	ApprovedBy     *string         `json:"approved_by"`
	RevokedAt      *time.Time      `json:"revoked_at"`
	LastSeenAt     *time.Time      `json:"last_seen_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Online         bool            `json:"online"`
	Workers        []NodeWorker    `json:"workers"`
	StageOverrides map[string]bool `json:"stage_overrides"`
}

func view(n cluster.Node, workers []cluster.NodeWorker, stages map[string]bool, now time.Time) Node {
	out := Node{
		ID: n.ID, Name: n.Name, State: n.State, Expired: expired(n, now),
		OS: n.OS, Arch: n.Arch,
		Versions: n.Versions, Capabilities: n.Capabilities, Storage: n.Storage,
		PathMap: n.PathMap, PathMapSource: n.PathMapSource, Drain: n.Drain,
		CodeAttempts: n.CodeAttempts, ExpiresAt: n.ExpiresAt, DBRole: n.DBRole, RemoteAddr: n.RemoteAddr,
		CreatedAt: n.CreatedAt, ApprovedAt: n.ApprovedAt, ApprovedBy: n.ApprovedBy, RevokedAt: n.RevokedAt,
		LastSeenAt: n.LastSeenAt, UpdatedAt: n.UpdatedAt,
		Workers: []NodeWorker{}, StageOverrides: map[string]bool{},
	}
	if out.PathMap == nil {
		out.PathMap = json.RawMessage("null")
	}
	if n.State == cluster.StatePending {
		out.AttemptsLeft = max(MaxCodeAttempts-n.CodeAttempts, 0)
	}
	live := func(t time.Time) bool { return now.Sub(t) <= OnlineWindow }
	if n.LastSeenAt != nil && live(*n.LastSeenAt) {
		out.Online = true
	}
	for _, w := range workers {
		nw := NodeWorker{ID: w.ID, Host: w.Host, Online: live(w.LastSeenAt), LastSeenAt: w.LastSeenAt,
			Stages: w.Stages, StagesOff: w.StagesOff}
		if nw.Stages == nil {
			nw.Stages = []string{}
		}
		if nw.StagesOff == nil {
			nw.StagesOff = json.RawMessage("{}")
		}
		if nw.Online {
			out.Online = true
		}
		if out.LastSeenAt == nil || w.LastSeenAt.After(*out.LastSeenAt) {
			t := w.LastSeenAt
			out.LastSeenAt = &t
		}
		out.Workers = append(out.Workers, nw)
	}
	for s, on := range stages {
		out.StageOverrides[s] = on
	}
	return out
}

func (h *Handler) views(ctx context.Context, now time.Time) ([]Node, error) {
	nodes, err := h.Store.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	stages, err := h.Store.Stages(ctx)
	if err != nil {
		return nil, err
	}
	workers, err := h.Store.Workers(ctx)
	if err != nil {
		return nil, err
	}
	byNode := map[string][]cluster.NodeWorker{}
	for _, w := range workers {
		byNode[w.NodeID] = append(byNode[w.NodeID], w)
	}
	out := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, view(n, byNode[n.ID], stages[n.ID], now))
	}
	return out, nil
}

// writeNode answers with the machine's current view.
func (h *Handler) writeNode(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	now := h.now()
	n, err := h.Store.Node(ctx, id)
	if errors.Is(err, cluster.ErrNotFound) {
		httperror.Write(w, r, httperror.NotFound("machine"))
		return
	} else if err != nil {
		errInternal(w, r, "read node", err)
		return
	}
	stages, err := h.Store.Stages(ctx)
	if err != nil {
		errInternal(w, r, "read stage switches", err)
		return
	}
	workers, err := h.Store.Workers(ctx)
	if err != nil {
		errInternal(w, r, "read workers", err)
		return
	}
	var mine []cluster.NodeWorker
	for _, wk := range workers {
		if wk.NodeID == id {
			mine = append(mine, wk)
		}
	}
	common.WriteJSON(w, r, http.StatusOK, view(n, mine, stages[id], now))
}
