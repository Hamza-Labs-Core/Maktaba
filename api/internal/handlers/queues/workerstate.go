package queues

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Stalled workers: jobs wait and no worker runs them. The pipeline's workers can stay up
// and do nothing (after the database went away and came back, for one), and the queues
// then look calm: nothing fails, nothing moves. GET /api/queues/pulse says so in its
// `workers` block, and an admin starts the workers again:
//
//	POST /api/queues/workers/restart   this machine's pipeline workers, through its supervisor
//
// The rule (workersState, pure):
//   - no_heartbeat: runnable jobs wait and no worker has written its registry heartbeat
//     (workers.last_seen_at, every 10 s) for workersSilentAfter;
//   - no_dispatch: a worker that beats serves the stage of a runnable job, no job runs,
//     and nothing was claimed, beaten or finished for workersIdleAfter, the job waiting
//     at least as long.
//
// A runnable job is pending, due, not parked on a dependency, its stage on and not
// paused. With every queue paused the workers are never called stalled.
const (
	// workersSilentAfter: twelve missed registry heartbeats.
	workersSilentAfter = 2 * time.Minute
	// workersIdleAfter: how long served, runnable work may wait with nothing moving.
	workersIdleAfter = pulseIdleAfter
)

// Workers states and reasons of WorkersState.
const (
	WorkersRunning       = "running"
	WorkersNotResponding = "not_responding"

	WorkersNoHeartbeat = "no_heartbeat"
	WorkersNoDispatch  = "no_dispatch"
)

// CodeWorkersRestartUnavailable: no supervisor to ask (the components routes' code).
const CodeWorkersRestartUnavailable = "components_unavailable"

// WorkersState is Pulse.workers: whether the workers run the waiting jobs.
type WorkersState struct {
	State string `json:"state"`
	// Reason is set while not_responding: no_heartbeat or no_dispatch.
	Reason string `json:"reason,omitempty"`
	// Since is when the workers stopped running jobs (not_responding only).
	Since *time.Time `json:"since"`
	// LastSeenAt is the newest worker heartbeat (null: no worker ever registered).
	LastSeenAt *time.Time `json:"last_seen_at"`
	// Waiting is the runnable jobs nothing runs.
	Waiting int `json:"waiting"`
	// CanRestart: this server runs under a supervisor that can restart the workers.
	CanRestart bool `json:"can_restart"`
}

// workersInputs is what workersState decides from.
type workersInputs struct {
	PausedAll bool
	Running   int
	// Waiting is the runnable jobs; Served those whose stage a beating worker serves.
	Waiting, Served int
	// WaitingSince and ServedSince are when the oldest of each became runnable.
	WaitingSince, ServedSince *time.Time
	// LastSeen is the newest registry heartbeat of any worker.
	LastSeen *time.Time
	// LastActivity is the newest job heartbeat or finish (the pulse's last activity).
	LastActivity *time.Time
}

func workersState(in workersInputs, now time.Time) WorkersState {
	out := WorkersState{State: WorkersRunning, LastSeenAt: utcPtr(in.LastSeen), Waiting: in.Waiting}
	if in.PausedAll || in.Waiting == 0 {
		return out
	}
	if in.LastSeen == nil || now.Sub(*in.LastSeen) > workersSilentAfter {
		out.State, out.Reason = WorkersNotResponding, WorkersNoHeartbeat
		// silent since the last beat, or since the work began to wait when that is later
		out.Since = utcPtr(latest(in.LastSeen, in.WaitingSince))
		return out
	}
	quiet := in.LastActivity == nil || now.Sub(*in.LastActivity) > workersIdleAfter
	if in.Served > 0 && in.Running == 0 && quiet &&
		in.ServedSince != nil && now.Sub(*in.ServedSince) >= workersIdleAfter {
		out.State, out.Reason = WorkersNotResponding, WorkersNoDispatch
		out.Since = utcPtr(latest(in.LastActivity, in.ServedSince))
	}
	return out
}

// latest is the later of two times (nil: the other).
func latest(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil || a.After(*b):
		return a
	}
	return b
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// workersStateSQL: $1 = now, $2 = the oldest registry heartbeat of a worker that still
// beats. One row always.
var workersStateSQL = `
WITH live AS (
    SELECT DISTINCT unnest(w.stages) AS stage FROM workers w WHERE w.last_seen_at >= $2)
SELECT (SELECT max(w.last_seen_at) FROM workers w),
       count(*),
       count(*) FILTER (WHERE l.stage IS NOT NULL),
       min(GREATEST(j.created_at, j.updated_at, COALESCE(j.not_before, j.created_at))),
       min(GREATEST(j.created_at, j.updated_at, COALESCE(j.not_before, j.created_at)))
           FILTER (WHERE l.stage IS NOT NULL)
  FROM processing_jobs j
  JOIN job_stages s ON s.stage = j.stage
  LEFT JOIN live l ON l.stage = j.stage
 WHERE j.state = 'pending' AND j.parked_on IS NULL
   AND (j.not_before IS NULL OR j.not_before <= $1)
   AND s.enabled AND NOT s.paused
   AND ` + components.NotRetiredStageSQL("j.stage")

// loadWorkersState reads the registry and the runnable jobs and applies the rule.
func (h *Handler) loadWorkersState(ctx context.Context, now time.Time, pausedAll bool, running int,
	lastActivity *time.Time) (WorkersState, error) {
	in := workersInputs{PausedAll: pausedAll, Running: running, LastActivity: lastActivity}
	var seen, waitingSince, servedSince sql.NullTime
	err := h.DB.QueryRowContext(ctx, workersStateSQL, now, now.Add(-workerOnlineWindow)).
		Scan(&seen, &in.Waiting, &in.Served, &waitingSince, &servedSince)
	if err != nil {
		return WorkersState{}, err
	}
	in.LastSeen, in.WaitingSince, in.ServedSince = nullTimePtr(seen), nullTimePtr(waitingSince), nullTimePtr(servedSince)
	out := workersState(in, now)
	out.CanRestart = h.RestartWorkers != nil
	return out, nil
}

// workersRestarted is the POST /api/queues/workers/restart answer.
type workersRestarted struct {
	// Restarted is how many worker processes the supervisor restarted (0: none runs
	// now; the supervisor starts them).
	Restarted int `json:"restarted"`
}

func workersUnavailable(detail string) *httperror.Error {
	return (&httperror.Error{
		Type:   httperror.TypeForCode(CodeWorkersRestartUnavailable),
		Title:  "the workers cannot be restarted",
		Status: http.StatusServiceUnavailable,
		Detail: detail,
	}).With("code", CodeWorkersRestartUnavailable)
}

// RestartWorkersNow implements POST /api/queues/workers/restart: an admin starts this
// machine's pipeline workers again (200 with how many, 202 when none runs now). 503
// components_unavailable with no supervisor, or one that does not answer. The restart
// is not tied to the stalled state: an admin may ask at any time.
func (h *Handler) RestartWorkersNow(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if h.RestartWorkers == nil {
		httperror.Write(w, r, workersUnavailable("this server runs under no supervisor"))
		return
	}
	n, err := h.RestartWorkers(r.Context())
	if err != nil {
		httperror.Write(w, r, workersUnavailable("the supervisor did not restart the workers").Wrap(err))
		return
	}
	if err := auditAdmin(r.Context(), h.DB, "queue.workers.restart", "workers", "",
		map[string]any{"restarted": n}); err != nil {
		writeErr(w, r, "audit", err)
		return
	}
	h.publish(r.Context(), "queue.control", map[string]any{"workers_restarted": n, "by": actor(r)})
	status := http.StatusOK
	if n == 0 {
		status = http.StatusAccepted
	}
	common.WriteJSON(w, r, status, workersRestarted{Restarted: n})
}
