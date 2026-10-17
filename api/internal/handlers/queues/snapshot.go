package queues

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
)

// Window used for the "rate" and flow calculations (plan-06-11 §8.2).
const rateWindow = 10 * time.Minute

// liveCounts is the aggregate of one stage's live rows.
type liveCounts struct {
	Pending, Ready, Backoff, Running, Paused int
	Since, Oldest                            *time.Time
	// BackoffCause is the error_cause of a backing-off row whose failure
	// was an unreachable service ("plex_unreachable"), if any.
	BackoffCause *string
}

func (c *liveCounts) live() int { return c.Pending + c.Running + c.Paused }

// doneCounts is the aggregate of one stage's recent done rows.
type doneCounts struct{ Done, Skipped, Recent int }

// stageInputs is everything summarizeStage needs; it is pure so the
// precedence rules are table-testable without a database.
type stageInputs struct {
	Info         StageInfo
	PausedAll    bool
	Live         liveCounts
	Done         doneCounts
	WaveDone     int
	FailedOpen   int
	Awaiting     int
	PoolBusy     int
	PoolSize     int
	UpstreamLive bool
	Running      []JobRef
	Served       Coverage
	Now          time.Time
}

// summarizeStage turns raw counters into the QueueSummary of §8.2/§8.3.
func summarizeStage(in stageInputs) QueueSummary {
	s := QueueSummary{Stage: in.Info.Stage, RunningJobs: in.Running}
	if s.RunningJobs == nil {
		s.RunningJobs = []JobRef{}
	}
	s.Counts = Counts{
		Pending:        in.Live.Pending,
		Ready:          in.Live.Ready,
		Running:        in.Live.Running,
		Paused:         in.Live.Paused,
		FailedOpen:     in.FailedOpen,
		Done24h:        in.Done.Done,
		Skipped24h:     in.Done.Skipped,
		AwaitingReview: in.Awaiting,
	}

	// Ring gauge: the "wave" of the current backlog.
	since := in.Now.Add(-24 * time.Hour)
	waveDone := in.Done.Done + in.Done.Skipped
	if in.Live.Since != nil {
		since = *in.Live.Since
		waveDone = in.WaveDone
	}
	s.Wave = Wave{Since: &since, Done: waveDone, Total: waveDone + in.Live.live() + in.FailedOpen}

	s.RatePerMin = float64(in.Done.Recent) / rateWindow.Minutes()
	if s.RatePerMin > 0 {
		eta := float64(in.Live.Ready) / s.RatePerMin * 60
		s.EtaSec = &eta
	}
	if in.Live.Oldest != nil {
		age := in.Now.Sub(*in.Live.Oldest).Seconds()
		if age < 0 {
			age = 0
		}
		s.OldestWaitingSec = &age
	}
	s.Extension = in.Info.Extension
	s.ServedBy = in.Served.Hosts
	if s.ServedBy == nil {
		s.ServedBy = []string{}
	}
	s.ServedWorkers = in.Served.Workers
	s.NoWorker = noWorker(in)
	s.RetryingService = retryingService(in.Live.BackoffCause)
	if in.Live.Running == 0 {
		s.WaitingReason = waitingReason(in)
	}
	return s
}

// noWorker: work is waiting for an enabled stage that no online worker
// serves ("waiting for a worker that serves transcribe").
func noWorker(in stageInputs) bool {
	return in.Info.Enabled && in.Live.Pending > 0 && in.Served.Workers == 0
}

// waitingReason applies the precedence of plan-06-11 §8.3. It returns
// nil when work is simply about to be claimed (ready rows, free slot).
func waitingReason(in stageInputs) *WaitingReason {
	pick := func(w WaitingReason) *WaitingReason { return &w }
	switch {
	case !in.Info.Enabled && in.Info.DisabledReason != nil && *in.Info.DisabledReason == "not_configured":
		return pick(WaitNotConfigured)
	case !in.Info.Enabled:
		return pick(WaitDisabled)
	case in.PausedAll:
		return pick(WaitPausedAll)
	case in.Info.Paused:
		return pick(WaitPaused)
	case in.Awaiting > 0 && in.Live.Pending == 0:
		return pick(WaitAwaitingReview)
	case noWorker(in):
		return pick(WaitNoWorker)
	case in.Live.Ready > 0 && in.PoolBusy >= in.PoolSize:
		return pick(WaitPoolFull)
	case in.Live.Pending > 0 && in.Live.Ready == 0 && in.Live.Backoff == 0:
		return pick(WaitDebouncing)
	case in.Live.Pending > 0 && in.Live.Ready == 0 && retryingService(in.Live.BackoffCause) != nil:
		return pick(WaitServiceRetrying)
	case in.Live.Pending > 0 && in.Live.Ready == 0:
		return pick(WaitBackoff)
	case in.Live.Pending == 0 && in.UpstreamLive:
		return pick(WaitUpstream)
	case in.Live.Pending == 0:
		return pick(WaitIdle)
	}
	return nil
}

// retryingService maps an unreachable-service cause to the service name
// ("plex_unreachable" -> "plex"); nil for anything else.
func retryingService(cause *string) *string {
	if cause == nil {
		return nil
	}
	name, ok := strings.CutSuffix(*cause, "_unreachable")
	if !ok || name == "" {
		return nil
	}
	return &name
}

const liveCountsSQL = `
SELECT stage,
       count(*) FILTER (WHERE state = 'pending'),
       count(*) FILTER (WHERE state = 'pending' AND (not_before IS NULL OR not_before <= $1)),
       count(*) FILTER (WHERE state = 'pending' AND not_before > $1 AND attempts > 0),
       count(*) FILTER (WHERE state IN ('claimed','running','resuming')),
       count(*) FILTER (WHERE state = 'paused'),
       min(created_at),
       min(created_at) FILTER (WHERE state = 'pending' AND (not_before IS NULL OR not_before <= $1)),
       max(error_cause) FILTER (WHERE state = 'pending' AND not_before > $1 AND attempts > 0
                                  AND error_cause LIKE '%unreachable')
  FROM processing_jobs
 WHERE state IN ('pending','claimed','running','resuming','paused')
 GROUP BY stage`

const doneCountsSQL = `
SELECT stage,
       count(*) FILTER (WHERE outcome IS DISTINCT FROM 'skipped_unchanged'),
       count(*) FILTER (WHERE outcome = 'skipped_unchanged'),
       count(*) FILTER (WHERE outcome IS DISTINCT FROM 'skipped_unchanged' AND finished_at > $2)
  FROM processing_jobs
 WHERE state = 'done' AND finished_at > $1
 GROUP BY stage`

// waveDoneSQL counts done rows (skipped included) since the oldest live
// row of each stage, so a re-queued library fills its ring quickly when
// nothing changed.
const waveDoneSQL = `
SELECT w.stage, count(*)
  FROM (SELECT stage, min(created_at) AS since
          FROM processing_jobs
         WHERE state IN ('pending','claimed','running','resuming','paused')
         GROUP BY stage) w
  JOIN processing_jobs j ON j.stage = w.stage AND j.state = 'done' AND j.finished_at >= w.since
 GROUP BY w.stage`

const runningJobsSQLTail = ` WHERE j.state IN ('claimed','running','resuming')`

const flowsSQL = `
SELECT triggered_by_stage, stage, count(*)
  FROM processing_jobs
 WHERE created_at > $1 AND triggered_by_stage IS NOT NULL
 GROUP BY triggered_by_stage, stage`

func (h *Handler) loadLiveCounts(ctx context.Context, now time.Time) (map[string]liveCounts, error) {
	rows, err := h.DB.QueryContext(ctx, liveCountsSQL, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]liveCounts{}
	for rows.Next() {
		var stage string
		var c liveCounts
		var since, oldest sql.NullTime
		var cause sql.NullString
		if err := rows.Scan(&stage, &c.Pending, &c.Ready, &c.Backoff, &c.Running, &c.Paused, &since, &oldest, &cause); err != nil {
			return nil, err
		}
		c.Since, c.Oldest = nullTimePtr(since), nullTimePtr(oldest)
		if cause.Valid {
			c.BackoffCause = &cause.String
		}
		out[stage] = c
	}
	return out, rows.Err()
}

func (h *Handler) loadDoneCounts(ctx context.Context, now time.Time) (map[string]doneCounts, error) {
	rows, err := h.DB.QueryContext(ctx, doneCountsSQL, now.Add(-24*time.Hour), now.Add(-rateWindow))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]doneCounts{}
	for rows.Next() {
		var stage string
		var c doneCounts
		if err := rows.Scan(&stage, &c.Done, &c.Skipped, &c.Recent); err != nil {
			return nil, err
		}
		out[stage] = c
	}
	return out, rows.Err()
}

func (h *Handler) loadWaveDone(ctx context.Context) (map[string]int, error) {
	rows, err := h.DB.QueryContext(ctx, waveDoneSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var stage string
		var n int
		if err := rows.Scan(&stage, &n); err != nil {
			return nil, err
		}
		out[stage] = n
	}
	return out, rows.Err()
}

// loadRunningJobs returns up to perStage in-flight jobs per stage (all
// of them when perStage <= 0), oldest first.
func (h *Handler) loadRunningJobs(ctx context.Context, perStage int, stage string) (map[string][]JobRef, error) {
	q := jobRefQuery("") + runningJobsSQLTail
	args := []any{}
	if stage != "" {
		q += ` AND j.stage = $1`
		args = append(args, stage)
	}
	q += ` ORDER BY j.started_at NULLS LAST, j.id`
	jobs, err := queryJobRefs(ctx, h.DB, q, args...)
	if err != nil {
		return nil, err
	}
	out := map[string][]JobRef{}
	for _, j := range jobs {
		if perStage > 0 && len(out[j.Stage]) >= perStage {
			continue
		}
		out[j.Stage] = append(out[j.Stage], j)
	}
	return out, nil
}

func (h *Handler) loadFlows(ctx context.Context, now time.Time) ([]Flow, error) {
	rows, err := h.DB.QueryContext(ctx, flowsSQL, now.Add(-rateWindow))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Flow{}
	for rows.Next() {
		var f Flow
		var n int
		if err := rows.Scan(&f.From, &f.To, &n); err != nil {
			return nil, err
		}
		f.PerMin = float64(n) / rateWindow.Minutes()
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out, nil
}

// BuildSnapshot computes the full Snapshot (GET /api/queues and
// the queues.snapshot event). Every count query filters on live states
// or a time window so the cost does not grow with history.
func (h *Handler) BuildSnapshot(ctx context.Context) (Snapshot, error) {
	now := h.now()
	stages, err := h.loadStages(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	pools, err := h.loadPools(ctx, stages)
	if err != nil {
		return Snapshot{}, err
	}
	pausedAll, err := h.pausedAll(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	live, err := h.loadLiveCounts(ctx, now)
	if err != nil {
		return Snapshot{}, err
	}
	done, err := h.loadDoneCounts(ctx, now)
	if err != nil {
		return Snapshot{}, err
	}
	wave, err := h.loadWaveDone(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	running, err := h.loadRunningJobs(ctx, 5, "")
	if err != nil {
		return Snapshot{}, err
	}
	groups, err := h.failureGroups(ctx, "", false, stages, now)
	if err != nil {
		return Snapshot{}, err
	}
	failed, awaiting, issues := tallyGroups(groups)
	workers, err := h.loadWorkers(ctx, now)
	if err != nil {
		return Snapshot{}, err
	}
	served := servedBy(workers)
	flows, err := h.loadFlows(ctx, now)
	if err != nil {
		return Snapshot{}, err
	}

	poolBy := map[string]Pool{}
	for _, p := range pools {
		poolBy[p.Pool] = p
	}
	queues := make([]QueueSummary, 0, len(stages))
	for _, st := range stages {
		upstream := false
		for _, f := range st.FedBy {
			if c, ok := live[f]; ok && c.live() > 0 {
				upstream = true
			}
		}
		p := poolBy[st.Pool]
		queues = append(queues, summarizeStage(stageInputs{
			Info: st, PausedAll: pausedAll, Live: live[st.Stage], Done: done[st.Stage],
			WaveDone: wave[st.Stage], FailedOpen: failed[st.Stage], Awaiting: awaiting[st.Stage],
			PoolBusy: p.Busy, PoolSize: p.EffectiveSize, UpstreamLive: upstream,
			Running: running[st.Stage], Served: served[st.Stage], Now: now,
		}))
	}

	hn, err := h.healthNow(ctx, now, pools, queues)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		At: now, PausedAll: pausedAll, Stages: stages, Queues: queues, Pools: pools,
		Flows: flows, Health: hn, IssuesCount: issues,
	}, nil
}

// Snapshot implements GET /api/queues.
func (h *Handler) Snapshot(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	snap, err := h.BuildSnapshot(r.Context())
	if err != nil {
		writeErr(w, r, "snapshot", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, snap)
}
