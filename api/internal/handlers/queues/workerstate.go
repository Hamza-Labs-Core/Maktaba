package queues

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Stalled workers: jobs wait and a worker that should run them does not. A pipeline
// worker can stay up and do nothing (after the database went away and came back, for
// one) while the others work, and the queues then look calm: nothing fails, its jobs do
// not move. GET /api/queues/pulse says which workers in its `workers` block, and an
// admin starts them again:
//
//	POST /api/queues/workers/restart   {"components": [...]}: those workers of this
//	                                   machine, through its supervisor; none named: all
//
// The rule is per worker (a row of the registry), stalledWorkers, pure. A worker is
// stalled when a job it could claim now has waited and it has not started one:
//
//   - a job it could claim: runnable (pending, due, not parked, no pause or cancel asked,
//     its stage on, not paused, not being flipped), of a stage the worker serves, whose
//     pool has a free slot. A pool shared by every worker is free while the jobs it runs
//     and the model's leases are fewer than its size; a per-worker pool is free while
//     this worker runs none of its jobs (its own size is the process's, which the
//     database does not hold). A pool of size 0 is never free;
//   - no_heartbeat: the worker has not written its registry heartbeat
//     (workers.last_seen_at, every 10 s) for workersSilentAfter, such a job waiting as
//     long;
//   - no_dispatch: the worker beats, and for workersIdleAfter it has been up, such a job
//     has waited, and it has started nothing.
//
// What a worker started is read from the jobs it holds now (claimed_by, claimed_at) and
// from the engine's `claimed` job event of each start ("started by <worker id>") within
// the bound: a finished job no longer names its worker, so there is no other record.
//
// With no worker registered at all and runnable jobs waiting, the state is
// not_responding with no worker named. With every queue paused nothing is stalled.
const (
	// workersSilentAfter: twelve missed registry heartbeats.
	workersSilentAfter = 2 * time.Minute
	// workersIdleAfter: how long a job a worker could claim may wait with the worker
	// starting nothing.
	workersIdleAfter = pulseIdleAfter
)

// Workers states and reasons of WorkersState.
const (
	WorkersRunning       = "running"
	WorkersNotResponding = "not_responding"

	WorkersNoHeartbeat = "no_heartbeat"
	WorkersNoDispatch  = "no_dispatch"
)

// Kinds of worker, by the catalog component of its process.
const (
	WorkerMain          = "main"
	WorkerTranscription = "transcription"
	WorkerSearch        = "search"

	componentPipeline      = "core.pipeline"
	componentTranscription = "transcription"
	componentSearch        = "search.semantic"
)

// CodeWorkersRestartUnavailable: no supervisor to ask (the components routes' code).
const CodeWorkersRestartUnavailable = "components_unavailable"

// WorkersState is Pulse.workers: whether the workers run the waiting jobs.
type WorkersState struct {
	State string `json:"state"`
	// Reason is set while not_responding: the first stalled worker's, or no_heartbeat
	// when no worker is registered.
	Reason string `json:"reason,omitempty"`
	// Since is when the first of them stopped running jobs (not_responding only).
	Since *time.Time `json:"since"`
	// LastSeenAt is the newest worker heartbeat (null: no worker ever registered).
	LastSeenAt *time.Time `json:"last_seen_at"`
	// Waiting is the runnable jobs the stalled workers could claim (with no worker
	// registered: every runnable job).
	Waiting int `json:"waiting"`
	// Stalled names the workers that do not run their jobs.
	Stalled []StalledWorker `json:"stalled"`
	// CanRestart: this server runs under a supervisor that can restart its workers.
	CanRestart bool `json:"can_restart"`
}

// StalledWorker is one worker that does not run the jobs it could.
type StalledWorker struct {
	WorkerID string `json:"worker_id"`
	Host     string `json:"host"`
	// NodeName is the joined machine it runs on (null: the home).
	NodeName *string `json:"node_name"`
	// Kind is main, transcription or search; Component the catalog component a restart
	// names (core.pipeline, transcription, search.semantic).
	Kind      string `json:"kind"`
	Component string `json:"component"`
	Reason    string `json:"reason"`
	// Since is when it stopped running jobs.
	Since      time.Time `json:"since"`
	LastSeenAt time.Time `json:"last_seen_at"`
	// Waiting is the jobs it could claim now; Stages their stages.
	Waiting int      `json:"waiting"`
	Stages  []string `json:"stages"`
	// Restartable: it runs on the home, whose supervisor the restart asks.
	Restartable bool `json:"restartable"`
}

// stageWait is the runnable jobs of one stage and its pool.
type stageWait struct {
	Stage, Pool string
	// PerWorker: the pool's slots are each worker's own.
	PerWorker bool
	// Size is the pool's size in the database (nil: none set, a per-worker pool sized
	// by its process).
	Size *int
	// Waiting jobs, the oldest runnable since Since.
	Waiting int
	Since   time.Time
}

// workersInputs is what stalledWorkers decides from.
type workersInputs struct {
	PausedAll bool
	Workers   []Worker
	Stages    []stageWait
	// Held is the in-flight jobs by worker id and pool; HeldAll by pool, with the
	// model's leases counted in.
	Held    map[string]map[string]int
	HeldAll map[string]int
	// Started is when each worker last started a job, as far as the bound reaches.
	Started map[string]time.Time
}

// poolFree reports whether worker w could take a job of s's pool now. own false leaves
// the worker's own jobs out (a silent worker's claims are stale).
func (in workersInputs) poolFree(w Worker, s stageWait, own bool) bool {
	if s.Size != nil && *s.Size <= 0 {
		return false
	}
	if s.PerWorker {
		return !own || in.Held[w.ID][s.Pool] == 0
	}
	size := 1
	if s.Size != nil {
		size = *s.Size
	}
	return in.HeldAll[s.Pool] < size
}

// workerKind is the kind of a worker and its catalog component, from the stages it
// serves: one that serves a stage of the core pipeline (or none the catalog knows) is
// the main worker; else it is transcription's or the search index's.
func workerKind(cat *components.Catalog, stages []string) (kind, component string) {
	seen := map[string]bool{}
	for _, s := range stages {
		if c, ok := cat.ByStage(s); ok {
			seen[c.ID] = true
		}
	}
	switch {
	case seen[componentPipeline]:
	case seen[componentTranscription]:
		return WorkerTranscription, componentTranscription
	case seen[componentSearch]:
		return WorkerSearch, componentSearch
	}
	return WorkerMain, componentPipeline
}

// stalledWorkers applies the rule to every worker.
func stalledWorkers(in workersInputs, now time.Time) []StalledWorker {
	out := []StalledWorker{}
	if in.PausedAll {
		return out
	}
	cat := components.Default()
	for _, w := range in.Workers {
		silent := now.Sub(w.LastSeenAt) > workersSilentAfter
		var stages []string
		var oldest time.Time
		waiting := 0
		for _, s := range in.Stages {
			if s.Waiting == 0 || !slices.Contains(w.Stages, s.Stage) || !in.poolFree(w, s, !silent) {
				continue
			}
			stages = append(stages, s.Stage)
			waiting += s.Waiting
			if oldest.IsZero() || s.Since.Before(oldest) {
				oldest = s.Since
			}
		}
		if waiting == 0 {
			continue
		}
		sw := StalledWorker{WorkerID: w.ID, Host: w.Host, NodeName: w.NodeName, LastSeenAt: w.LastSeenAt.UTC(),
			Waiting: waiting, Stages: stages, Restartable: w.NodeID == nil || *w.NodeID == ""}
		sw.Kind, sw.Component = workerKind(cat, w.Stages)
		sort.Strings(sw.Stages)
		if silent {
			since := laterOf(w.LastSeenAt, oldest)
			if now.Sub(since) < workersSilentAfter {
				continue
			}
			sw.Reason, sw.Since = WorkersNoHeartbeat, since.UTC()
		} else {
			since := laterOf(laterOf(w.StartedAt, oldest), in.Started[w.ID])
			if now.Sub(since) < workersIdleAfter {
				continue
			}
			sw.Reason, sw.Since = WorkersNoDispatch, since.UTC()
		}
		out = append(out, sw)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	return out
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// workersState folds the stalled workers into the pulse's block.
func workersState(in workersInputs, now time.Time) WorkersState {
	out := WorkersState{State: WorkersRunning, Stalled: stalledWorkers(in, now)}
	for _, w := range in.Workers {
		if out.LastSeenAt == nil || w.LastSeenAt.After(*out.LastSeenAt) {
			t := w.LastSeenAt.UTC()
			out.LastSeenAt = &t
		}
	}
	if in.PausedAll {
		return out
	}
	if len(in.Workers) == 0 {
		// no worker ever registered (or all were pruned): every runnable job waits
		var oldest time.Time
		for _, s := range in.Stages {
			out.Waiting += s.Waiting
			if s.Waiting > 0 && (oldest.IsZero() || s.Since.Before(oldest)) {
				oldest = s.Since
			}
		}
		if out.Waiting > 0 && now.Sub(oldest) >= workersSilentAfter {
			t := oldest.UTC()
			out.State, out.Reason, out.Since = WorkersNotResponding, WorkersNoHeartbeat, &t
		} else {
			out.Waiting = 0
		}
		return out
	}
	if len(out.Stalled) == 0 {
		return out
	}
	first := out.Stalled[0]
	out.State, out.Reason, out.Since = WorkersNotResponding, first.Reason, &first.Since
	// a job two stalled workers could both claim is counted once
	counted := map[string]bool{}
	for _, sw := range out.Stalled {
		for _, st := range sw.Stages {
			counted[st] = true
		}
	}
	for _, s := range in.Stages {
		if counted[s.Stage] {
			out.Waiting += s.Waiting
		}
	}
	return out
}

// stageWaitSQL: the runnable jobs by stage, with the stage's pool ($1 = now).
var stageWaitSQL = `
SELECT j.stage, s.pool, COALESCE(p.scope, 'global'), COALESCE(p.size, p.default_size), count(*),
       min(GREATEST(j.created_at, j.updated_at, COALESCE(j.not_before, j.created_at)))
  FROM processing_jobs j
  JOIN job_stages s ON s.stage = j.stage
  LEFT JOIN worker_pools p ON p.pool = s.pool
 WHERE j.state = 'pending' AND j.parked_on IS NULL
   AND NOT j.pause_requested AND NOT j.cancel_requested
   AND (j.not_before IS NULL OR j.not_before <= $1)
   AND s.enabled AND NOT s.paused AND NOT s.flipping
   AND ` + components.NotRetiredStageSQL("j.stage") + `
 GROUP BY 1, 2, 3, 4`

// workersHeldSQL: the in-flight jobs by worker and pool, and each worker's newest claim.
const workersHeldSQL = `
SELECT COALESCE(j.claimed_by, ''), s.pool, count(*), max(j.claimed_at)
  FROM processing_jobs j JOIN job_stages s ON s.stage = j.stage
 WHERE j.state IN ` + inFlightSQL + `
 GROUP BY 1, 2`

// workersLeasesSQL: the slots a plugin's question to the model holds ($1 = now).
const workersLeasesSQL = `SELECT pool, count(*) FROM compute_leases WHERE expires_at > $1 GROUP BY pool`

// workersStartedSQL: the engine's event of each job start since $1, by its text.
const workersStartedSQL = `
SELECT message, max(at) FROM job_events WHERE at > $1 AND kind = 'claimed' GROUP BY message`

// startedBy is the text of the engine's `claimed` event before the worker's id.
const startedBy = "started by "

// loadWorkersInputs reads the registry, the runnable jobs and what each worker holds.
func (h *Handler) loadWorkersInputs(ctx context.Context, now time.Time, pausedAll bool) (workersInputs, error) {
	in := workersInputs{PausedAll: pausedAll, Held: map[string]map[string]int{}, HeldAll: map[string]int{},
		Started: map[string]time.Time{}}
	var err error
	if in.Workers, err = h.loadWorkers(ctx, now); err != nil {
		return in, err
	}
	rows, err := h.DB.QueryContext(ctx, stageWaitSQL, now)
	if err != nil {
		return in, err
	}
	for rows.Next() {
		var s stageWait
		var scope string
		var size sql.NullInt64
		if err := rows.Scan(&s.Stage, &s.Pool, &scope, &size, &s.Waiting, &s.Since); err != nil {
			rows.Close()
			return in, err
		}
		s.PerWorker = scope == "per_worker"
		if size.Valid {
			n := int(size.Int64)
			s.Size = &n
		}
		in.Stages = append(in.Stages, s)
	}
	if err := closeRows(rows); err != nil {
		return in, err
	}
	if len(in.Stages) == 0 {
		return in, nil // nothing waits: nothing can be stalled
	}

	rows, err = h.DB.QueryContext(ctx, workersHeldSQL)
	if err != nil {
		return in, err
	}
	for rows.Next() {
		var id, pool string
		var n int
		var claimed sql.NullTime
		if err := rows.Scan(&id, &pool, &n, &claimed); err != nil {
			rows.Close()
			return in, err
		}
		in.HeldAll[pool] += n
		if id == "" {
			continue
		}
		if in.Held[id] == nil {
			in.Held[id] = map[string]int{}
		}
		in.Held[id][pool] += n
		if claimed.Valid && claimed.Time.After(in.Started[id]) {
			in.Started[id] = claimed.Time
		}
	}
	if err := closeRows(rows); err != nil {
		return in, err
	}
	if err := h.scanCounts(ctx, workersLeasesSQL, in.HeldAll, now); err != nil {
		return in, err
	}

	rows, err = h.DB.QueryContext(ctx, workersStartedSQL, now.Add(-workersIdleAfter))
	if err != nil {
		return in, err
	}
	for rows.Next() {
		var msg string
		var at time.Time
		if err := rows.Scan(&msg, &at); err != nil {
			rows.Close()
			return in, err
		}
		if id, ok := strings.CutPrefix(msg, startedBy); ok && at.After(in.Started[id]) {
			in.Started[id] = at
		}
	}
	return in, closeRows(rows)
}

// loadWorkersState reads what the rule needs and applies it.
func (h *Handler) loadWorkersState(ctx context.Context, now time.Time, pausedAll bool) (WorkersState, error) {
	in, err := h.loadWorkersInputs(ctx, now, pausedAll)
	if err != nil {
		return WorkersState{}, err
	}
	out := workersState(in, now)
	out.CanRestart = h.RestartWorkers != nil
	if !out.CanRestart {
		for i := range out.Stalled {
			out.Stalled[i].Restartable = false
		}
	}
	return out, nil
}

// WorkersNow is the pulse's workers block on its own (the System health page).
func (h *Handler) WorkersNow(ctx context.Context) (WorkersState, error) {
	paused, err := h.pausedAll(ctx)
	if err != nil {
		return WorkersState{}, err
	}
	return h.loadWorkersState(ctx, h.now(), paused)
}

// workersRestart is the optional POST /api/queues/workers/restart body.
type workersRestart struct {
	// Components to restart (core.pipeline, transcription, search.semantic); none: all.
	Components []string `json:"components"`
}

// workersRestarted is its answer.
type workersRestarted struct {
	// Restarted is how many worker processes the supervisor restarted (0: none runs
	// now; the supervisor starts them).
	Restarted  int      `json:"restarted"`
	Components []string `json:"components"`
}

// workerComponents are the components a restart may name.
var workerComponents = []string{componentPipeline, componentTranscription, componentSearch}

func workersUnavailable(detail string) *httperror.Error {
	return (&httperror.Error{
		Type:   httperror.TypeForCode(CodeWorkersRestartUnavailable),
		Title:  "the workers cannot be restarted",
		Status: http.StatusServiceUnavailable,
		Detail: detail,
	}).With("code", CodeWorkersRestartUnavailable)
}

// RestartWorkersNow implements POST /api/queues/workers/restart: an admin starts this
// machine's pipeline workers again, those of the components the body names or all of
// them (200 with how many, 202 when none runs now). 400 for a component that has no
// worker; 503 components_unavailable with no supervisor, or one that does not answer.
// The restart is not tied to the stalled state: an admin may ask at any time.
func (h *Handler) RestartWorkersNow(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var req workersRestart
	if r.Body != nil {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil || (len(strings.TrimSpace(string(raw))) > 0 && json.Unmarshal(raw, &req) != nil) {
			httperror.Write(w, r, httperror.BadRequest("the body is not JSON"))
			return
		}
	}
	ids := []string{}
	for _, id := range req.Components {
		if !slices.Contains(workerComponents, id) {
			httperror.Write(w, r, httperror.BadRequest(id+" has no worker to restart"))
			return
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if h.RestartWorkers == nil {
		httperror.Write(w, r, workersUnavailable("this server runs under no supervisor"))
		return
	}
	n, err := h.RestartWorkers(r.Context(), ids)
	if err != nil {
		httperror.Write(w, r, workersUnavailable("the supervisor did not restart the workers").Wrap(err))
		return
	}
	if err := auditAdmin(r.Context(), h.DB, "queue.workers.restart", "workers", "",
		map[string]any{"restarted": n, "components": ids}); err != nil {
		writeErr(w, r, "audit", err)
		return
	}
	h.publish(r.Context(), "queue.control", map[string]any{"workers_restarted": n, "components": ids, "by": actor(r)})
	status := http.StatusOK
	if n == 0 {
		status = http.StatusAccepted
	}
	common.WriteJSON(w, r, status, workersRestarted{Restarted: n, Components: ids})
}
