package queues

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

var laneRank = map[string]int{"sources": 0, "episodes": 1, "seasons": 2, "channels": 3, "media": 4}

// sortStages orders stages by lane, lane_order, name (stable UI order).
func sortStages(s []StageInfo) {
	sort.SliceStable(s, func(i, j int) bool {
		a, b := s[i], s[j]
		if laneRank[a.Lane] != laneRank[b.Lane] {
			return laneRank[a.Lane] < laneRank[b.Lane]
		}
		if a.LaneOrder != b.LaneOrder {
			return a.LaneOrder < b.LaneOrder
		}
		return a.Stage < b.Stage
	})
}

func parseEdges(raw []byte) []Edge {
	out := []Edge{}
	if len(raw) == 0 {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return []Edge{}
	}
	for i := range out {
		if out[i].When == "" {
			out[i].When = "output_changed"
		}
	}
	return out
}

// fillFedBy computes, for every stage, the stages with an edge or an
// explicit request into it (plan-06-11 StageInfo.fed_by).
func fillFedBy(stages []StageInfo) {
	idx := map[string]int{}
	for i, s := range stages {
		idx[s.Stage] = i
	}
	sets := make([]map[string]bool, len(stages))
	add := func(to, from string) {
		i, ok := idx[to]
		if !ok || to == from {
			return
		}
		if sets[i] == nil {
			sets[i] = map[string]bool{}
		}
		sets[i][from] = true
	}
	for _, s := range stages {
		for _, e := range s.Edges {
			add(e.To, s.Stage)
		}
		for _, to := range s.MayRequest {
			add(to, s.Stage)
		}
	}
	for i := range stages {
		names := make([]string, 0, len(sets[i]))
		for n := range sets[i] {
			names = append(names, n)
		}
		sort.Strings(names)
		stages[i].FedBy = names
	}
}

// effectiveSize is how many slots a pool offers. NULL size falls back
// to default_size and, for the per-worker cpu pool, to cores-1 (min 1)
// exactly like the claim SQL / runner (plan-06-11 §4.3, §5.3).
func effectiveSize(pool string, size, def *int, cpus int) int {
	if size != nil {
		return *size
	}
	if def != nil {
		return *def
	}
	if pool == "cpu" {
		if cpus-1 < 1 {
			return 1
		}
		return cpus - 1
	}
	return 1
}

// loadPools reads worker_pools plus live busy counts and the stage list
// of every pool.
func (h *Handler) loadPools(ctx context.Context, stages []StageInfo) ([]Pool, error) {
	rows, err := h.DB.QueryContext(ctx, `SELECT pool, label, scope, size, default_size, updated_at, updated_by
		FROM worker_pools ORDER BY pool`)
	if err != nil {
		return nil, err
	}
	defs := map[string]*int{}
	pools := []Pool{}
	for rows.Next() {
		var p Pool
		var size, dsize sql.NullInt64
		var by sql.NullString
		if err := rows.Scan(&p.Pool, &p.Label, &p.Scope, &size, &dsize, &p.UpdatedAt, &by); err != nil {
			rows.Close()
			return nil, err
		}
		if size.Valid {
			v := int(size.Int64)
			p.Size = &v
		}
		if dsize.Valid {
			v := int(dsize.Int64)
			defs[p.Pool] = &v
		}
		p.UpdatedBy = nullStrPtr(by)
		p.Stages = []string{}
		pools = append(pools, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	busy := map[string]int{}
	brows, err := h.DB.QueryContext(ctx, `SELECT s.pool, count(*)
		FROM processing_jobs j JOIN job_stages s ON s.stage = j.stage
		WHERE j.state IN `+inFlightSQL+` GROUP BY s.pool`)
	if err != nil {
		return nil, err
	}
	defer brows.Close()
	for brows.Next() {
		var pool string
		var n int
		if err := brows.Scan(&pool, &n); err != nil {
			return nil, err
		}
		busy[pool] = n
	}
	if err := brows.Err(); err != nil {
		return nil, err
	}

	byPool := map[string][]string{}
	for _, s := range stages {
		byPool[s.Pool] = append(byPool[s.Pool], s.Stage)
	}
	cpus := h.cpus()
	for i := range pools {
		p := &pools[i]
		p.EffectiveSize = effectiveSize(p.Pool, p.Size, defs[p.Pool], cpus)
		p.Busy = busy[p.Pool]
		if st := byPool[p.Pool]; st != nil {
			p.Stages = st
		}
	}
	return pools, nil
}

// ListPools implements GET /api/pools.
func (h *Handler) ListPools(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	stages, err := h.loadStages(r.Context())
	if err != nil {
		writeErr(w, r, "load stages", err)
		return
	}
	pools, err := h.loadPools(r.Context(), stages)
	if err != nil {
		writeErr(w, r, "load pools", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"pools": pools})
}

type putPoolBody struct {
	Size json.RawMessage `json:"size"`
}

// PutPool implements PUT /api/pools/{pool}: {size: 0..64 | null}.
func (h *Handler) PutPool(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	pool := chi.URLParam(r, "pool")
	var body putPoolBody
	if e := common.ReadJSON(r, &body, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if body.Size == nil {
		httperror.Write(w, r, httperror.BadRequest("size is required (a number 0-64, or null for auto)"))
		return
	}
	var size any // nil -> SQL NULL
	var sizePtr *int
	if string(body.Size) != "null" {
		var n int
		if err := json.Unmarshal(body.Size, &n); err != nil {
			httperror.Write(w, r, httperror.BadRequest("size must be an integer 0-64 or null"))
			return
		}
		if n < 0 || n > 64 {
			httperror.Write(w, r, httperror.BadRequest("size must be between 0 and 64"))
			return
		}
		size, sizePtr = n, &n
	}
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE worker_pools SET size = $2, updated_at = $3, updated_by = $4 WHERE pool = $1`,
		pool, size, h.now(), actor(r))
	if err != nil {
		writeErr(w, r, "update pool", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httperror.Write(w, r, httperror.NotFound("pool "+pool))
		return
	}
	stages, err := h.loadStages(r.Context())
	if err != nil {
		writeErr(w, r, "load stages", err)
		return
	}
	pools, err := h.loadPools(r.Context(), stages)
	if err != nil {
		writeErr(w, r, "load pools", err)
		return
	}
	h.publish(r.Context(), "pool.updated", map[string]any{"pool": pool, "size": sizePtr})
	for _, p := range pools {
		if p.Pool == pool {
			common.WriteJSON(w, r, http.StatusOK, p)
			return
		}
	}
	httperror.Write(w, r, httperror.NotFound("pool "+pool))
}

var errNoRows = sql.ErrNoRows

func isNoRows(err error) bool { return errors.Is(err, errNoRows) }
