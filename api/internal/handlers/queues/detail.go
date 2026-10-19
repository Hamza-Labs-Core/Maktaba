package queues

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

var jobStates = map[string]bool{
	"pending": true, "claimed": true, "running": true, "paused": true, "resuming": true,
	"done": true, "failed": true, "cancelled": true, "superseded": true,
}

const kpiSQL = `
SELECT count(*) FILTER (WHERE state = 'done'),
       count(*) FILTER (WHERE state = 'failed'),
       percentile_cont(0.5)  WITHIN GROUP (ORDER BY dur_ms)
           FILTER (WHERE state = 'done' AND outcome IS DISTINCT FROM 'skipped_unchanged' AND dur_ms IS NOT NULL),
       percentile_cont(0.95) WITHIN GROUP (ORDER BY dur_ms)
           FILTER (WHERE state = 'done' AND outcome IS DISTINCT FROM 'skipped_unchanged' AND dur_ms IS NOT NULL),
       count(*) FILTER (WHERE outcome = 'skipped_unchanged'),
       count(*) FILTER (WHERE outcome = 'changed')
  FROM (SELECT state, outcome,
               EXTRACT(EPOCH FROM (finished_at - started_at)) * 1000 AS dur_ms
          FROM processing_jobs
         WHERE stage = $1 AND finished_at > $2 AND state IN ('done', 'failed')) x`

// Graph implements GET /api/queues/graph.
func (h *Handler) Graph(w http.ResponseWriter, r *http.Request) {
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
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"stages": stages, "pools": pools})
}

// Detail implements GET /api/queues/{stage}.
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	ctx := r.Context()
	p := principal.FromContext(ctx)
	stage := chi.URLParam(r, "stage")
	info, ok := h.stageOr404(w, r, stage)
	if !ok {
		return
	}
	snap, err := h.BuildSnapshot(ctx)
	if err != nil {
		writeErr(w, r, "snapshot", err)
		return
	}
	var summary QueueSummary
	for _, q := range snap.Queues {
		if q.Stage == stage {
			summary = q
		}
	}
	info = pickStage(snap.Stages, stage, info)

	running, err := h.loadRunningJobs(ctx, 0, stage)
	if err != nil {
		writeErr(w, r, "running jobs", err)
		return
	}
	nextUp, err := queryJobRefs(ctx, h.DB, jobRefQuery("")+`
	 WHERE j.stage = $1 AND j.state = 'pending' AND (j.not_before IS NULL OR j.not_before <= $2)
	 ORDER BY j.priority, j.id LIMIT 20`, stage, h.now())
	if err != nil {
		writeErr(w, r, "next up", err)
		return
	}
	failures, err := h.failureGroups(ctx, stage, true, snap.Stages, h.now())
	if err != nil {
		writeErr(w, r, "failures", err)
		return
	}
	kpis, err := h.loadKPIs(r, stage)
	if err != nil {
		writeErr(w, r, "kpis", err)
		return
	}
	lines, err := h.logLines(ctx, logFilter{Stage: stage, MinLevel: "info", Limit: 100, Scope: libraryScope(p)})
	if err != nil {
		writeErr(w, r, "log", err)
		return
	}
	run := running[stage]
	if run == nil {
		run = []JobRef{}
	}
	summary.RunningJobs = filterRefs(p, summary.RunningJobs)
	common.WriteJSON(w, r, http.StatusOK, QueueDetail{
		Info: info, Summary: summary, KPIs: kpis, Running: filterRefs(p, run), NextUp: filterRefs(p, nextUp),
		Failures: scopeFailures(p, failures), Log: lines,
	})
}

func pickStage(all []StageInfo, stage string, fallback StageInfo) StageInfo {
	for _, s := range all {
		if s.Stage == stage {
			return s
		}
	}
	return fallback
}

func (h *Handler) loadKPIs(r *http.Request, stage string) (KPIs, error) {
	var k KPIs
	var med, p95 *float64
	var skipped, changed int
	err := h.DB.QueryRowContext(r.Context(), kpiSQL, stage, h.now().Add(-24*time.Hour)).
		Scan(&k.Done24h, &k.Failed24h, &med, &p95, &skipped, &changed)
	if err != nil {
		return k, err
	}
	k.MedianDurationMs, k.P95DurationMs = med, p95
	if k.Done24h > 0 {
		k.SkippedShare24h = float64(skipped) / float64(k.Done24h)
		k.ChangedShare24h = float64(changed) / float64(k.Done24h)
	}
	return k, nil
}

// Jobs implements GET /api/queues/{stage}/jobs (keyset by id, newest
// first; cursor is the id of the last row of the previous page).
func (h *Handler) Jobs(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	stage := chi.URLParam(r, "stage")
	if _, ok := h.stageOr404(w, r, stage); !ok {
		return
	}
	limit, e := common.QueryInt(r, "limit", 50)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	cursor, ok := parseCursor(r.URL.Query().Get("cursor"))
	if !ok {
		httperror.Write(w, r, httperror.InvalidQuery("cursor must be a job id"))
		return
	}
	states := []string{}
	for _, s := range common.QueryCSV(r, "state") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !jobStates[s] {
			httperror.Write(w, r, httperror.InvalidQuery("unknown state "+strconv.Quote(s)))
			return
		}
		states = append(states, s)
	}

	args := []any{stage}
	q := jobRefQuery("") + ` WHERE j.stage = $1`
	if scope := libraryScope(principal.FromContext(r.Context())); scope != nil {
		// a caller without every library lists only its libraries' jobs
		args = append(args, scope)
		q += ` AND j.library_id = ANY($` + strconv.Itoa(len(args)) + `::uuid[])`
	}
	if len(states) > 0 {
		args = append(args, pq.Array(states))
		q += ` AND j.state = ANY($` + strconv.Itoa(len(args)) + `::text[])`
	}
	if cursor > 0 {
		args = append(args, cursor)
		q += ` AND j.id < $` + strconv.Itoa(len(args))
	}
	args = append(args, limit+1)
	q += ` ORDER BY j.id DESC LIMIT $` + strconv.Itoa(len(args))

	items, err := queryJobRefs(r.Context(), h.DB, q, args...)
	if err != nil {
		writeErr(w, r, "list jobs", err)
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := strconv.FormatInt(items[len(items)-1].ID, 10)
		next = &c
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items, "next": next})
}
