package queues

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
)

// workerOnlineWindow: a worker whose last_seen_at is older than this is
// reported offline (workers heartbeat every few seconds).
const workerOnlineWindow = 60 * time.Second

// Registry shape (plan-06-11 "Workstream B notes"; Epic 31 adds the
// machine a worker runs on): workers(id TEXT PK, host TEXT, stages TEXT[],
// pools, started_at, last_seen_at, version TEXT, node_id uuid NULL,
// role TEXT), node_name from cluster_nodes (null for a home worker).
const workersSQL = `
SELECT w.id, w.host, w.stages, w.pools, w.started_at, w.last_seen_at, w.version,
       w.node_id::text, cn.name, w.role
  FROM workers w LEFT JOIN cluster_nodes cn ON cn.id = w.node_id
 ORDER BY w.host, w.id`

func (h *Handler) loadWorkers(ctx context.Context, now time.Time) ([]Worker, error) {
	return LoadWorkers(ctx, h.DB, now)
}

// LoadWorkers reads the workers registry and marks liveness.
func LoadWorkers(ctx context.Context, db *sql.DB, now time.Time) ([]Worker, error) {
	rows, err := db.QueryContext(ctx, workersSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Worker{}
	for rows.Next() {
		var w Worker
		var stages pq.StringArray
		var pools []byte
		var version, nodeID, nodeName, role sql.NullString
		if err := rows.Scan(&w.ID, &w.Host, &stages, &pools, &w.StartedAt, &w.LastSeenAt, &version,
			&nodeID, &nodeName, &role); err != nil {
			return nil, err
		}
		w.NodeID, w.NodeName = nullStrPtr(nodeID), nullStrPtr(nodeName)
		w.Role = "home"
		if role.Valid && role.String != "" {
			w.Role = role.String
		}
		w.Stages = nonNilStrings(stages)
		w.Pools = rawOrNull(pools)
		if !json.Valid(w.Pools) {
			w.Pools = json.RawMessage("null")
		}
		w.Version = nullStrPtr(version)
		w.Online = now.Sub(w.LastSeenAt) <= workerOnlineWindow
		out = append(out, w)
	}
	return out, rows.Err()
}

// ServedByStage computes per-stage live coverage from the online workers.
func ServedByStage(workers []Worker) map[string]Coverage { return servedBy(workers) }

func servedBy(workers []Worker) map[string]Coverage {
	hosts := map[string]map[string]bool{}
	count := map[string]int{}
	for _, w := range workers {
		if !w.Online {
			continue
		}
		for _, s := range w.Stages {
			count[s]++
			if hosts[s] == nil {
				hosts[s] = map[string]bool{}
			}
			hosts[s][w.Host] = true
		}
	}
	out := map[string]Coverage{}
	for s, n := range count {
		hs := make([]string, 0, len(hosts[s]))
		for h := range hosts[s] {
			hs = append(hs, h)
		}
		sort.Strings(hs)
		out[s] = Coverage{Workers: n, Hosts: hs}
	}
	return out
}

// Workers implements GET /api/workers.
func (h *Handler) Workers(w http.ResponseWriter, r *http.Request) {
	// host names, addresses and versions are operator detail
	if !requireAdmin(w, r) {
		return
	}
	ws, err := h.WorkersView(r.Context())
	if err != nil {
		writeErr(w, r, "workers", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"workers": ws})
}

// WorkersView is the GET /api/workers list, serve labels included (the
// cluster hub streams it to the Processing page).
func (h *Handler) WorkersView(ctx context.Context) ([]Worker, error) {
	ws, err := h.loadWorkers(ctx, h.now())
	if err != nil {
		return nil, err
	}
	labels := map[string]string{}
	if stages, err := h.loadStages(ctx); err == nil {
		for _, s := range stages {
			labels[s.Stage] = s.Label
		}
	}
	for i := range ws {
		ws[i].ServesLabel = servesLabel(ws[i].Stages, labels)
	}
	return ws, nil
}

// servesLabel renders "Probe, Detect, +3 more" from stage labels.
func servesLabel(stages []string, labels map[string]string) string {
	const maxNamed = 4
	names := make([]string, 0, len(stages))
	for _, s := range stages {
		if l := labels[s]; l != "" {
			names = append(names, l)
		} else {
			names = append(names, s)
		}
	}
	if len(names) <= maxNamed {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:maxNamed], ", ") + ", +" + strconv.Itoa(len(names)-maxNamed) + " more"
}
