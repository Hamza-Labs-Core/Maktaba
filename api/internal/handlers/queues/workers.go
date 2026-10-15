package queues

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
)

// workerOnlineWindow: a worker whose last_seen_at is older than this is
// reported offline (workers heartbeat every few seconds).
const workerOnlineWindow = 60 * time.Second

// Assumed registry shape (workstream A, plan-06-11 "Workstream B notes"):
// workers(id TEXT PK, host TEXT, stages TEXT[], pools JSONB,
// started_at, last_seen_at, version TEXT).
const workersSQL = `
SELECT id, host, stages, pools, started_at, last_seen_at, version
  FROM workers ORDER BY host, id`

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
		var version sql.NullString
		if err := rows.Scan(&w.ID, &w.Host, &stages, &pools, &w.StartedAt, &w.LastSeenAt, &version); err != nil {
			return nil, err
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
func ServedByStage(workers []Worker) map[string]ServedBy { return servedBy(workers) }

func servedBy(workers []Worker) map[string]ServedBy {
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
	out := map[string]ServedBy{}
	for s, n := range count {
		hs := make([]string, 0, len(hosts[s]))
		for h := range hosts[s] {
			hs = append(hs, h)
		}
		sort.Strings(hs)
		out[s] = ServedBy{Workers: n, Hosts: hs}
	}
	return out
}

// Workers implements GET /api/workers.
func (h *Handler) Workers(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	ws, err := h.loadWorkers(r.Context(), h.now())
	if err != nil {
		writeErr(w, r, "workers", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"workers": ws})
}
