// Package jobs implements Stories 7.12 + 7.13:
//
//	POST /api/jobs/{id}/pause     (and ?force=true)
//	POST /api/jobs/{id}/resume
//	POST /api/jobs/{id}/cancel
//	POST /api/jobs/{id}/retry
//	POST /api/videos/{id}/pause
//	POST /api/videos/{id}/resume
//	POST /api/videos/{id}/cancel
//	GET  /api/jobs/{id}
//	GET  /api/queue/stats
//
// All control endpoints set flags in one UPDATE and return immediately —
// the worker observes them async (Pipeline §7.7). The state-transition
// guard for “retry“ rejects non-failed jobs with 409.
package jobs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/paginate"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
)

// Job is the over-the-wire shape; minimal — handlers don't return rich
// objects, mostly status flags so the UI can re-render.
type Job struct {
	ID                    string     `json:"id"`
	VideoID               *string    `json:"video_id,omitempty"`
	LibraryID             *string    `json:"library_id,omitempty"`
	Stage                 string     `json:"stage"`
	State                 string     `json:"state"`
	Priority              int        `json:"priority"`
	Attempts              int        `json:"attempts"`
	PauseRequested        bool       `json:"pause_requested"`
	CancelRequested       bool       `json:"cancel_requested"`
	CreatedAt             time.Time  `json:"created_at"`
	NotBefore             *time.Time `json:"not_before,omitempty"`
	ClaimedBy             *string    `json:"claimed_by,omitempty"`
	LastHeartbeatAt       *time.Time `json:"last_heartbeat_at,omitempty"`
	EstimatedRemainingSec *float64   `json:"estimated_remaining_sec,omitempty"`
	Error                 *string    `json:"error,omitempty"`
	SubjectKind           string     `json:"subject_kind,omitempty"`
	SubjectID             string     `json:"subject_id,omitempty"`
}

// jobColumns is the SELECT list scanJob expects. error and
// estimated_remaining_sec used to be declared on Job but never
// selected; the subject pair falls back to the video for rows written
// before the subject columns were backfilled.
const jobColumns = `id, video_id, library_id, stage, state, priority, attempts,
	             pause_requested, cancel_requested, created_at, not_before,
	             claimed_by, last_heartbeat_at, error, estimated_remaining_sec,
	             COALESCE(subject_kind, 'video'), COALESCE(subject_id, video_id)::text`

// Handler bundles deps.
type Handler struct {
	DB      *sql.DB
	NowFunc func() time.Time
	// Settings holds the runtime pause grace (pause.grace_sec). Unset: a
	// force pause is taken at any time. Set: a force pause is taken only
	// for a job whose requested pause has waited that long (the worker is
	// stuck inside one segment; architecture §7.7 "Force pause").
	Settings *runtimecfg.Reader
}

// Mount wires the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/jobs/{id}", h.Get)
	r.Post("/api/jobs/{id}/pause", h.Pause)
	r.Post("/api/jobs/{id}/resume", h.Resume)
	r.Post("/api/jobs/{id}/cancel", h.Cancel)
	r.Post("/api/jobs/{id}/retry", h.Retry)
	r.Post("/api/videos/{id}/pause", h.PauseVideo)
	r.Post("/api/videos/{id}/resume", h.ResumeVideo)
	r.Post("/api/videos/{id}/cancel", h.CancelVideo)
	r.Get("/api/queue/stats", h.Stats)
	r.Get("/api/jobs", h.List)
}

// Get loads a single job by id.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	j, err := h.loadJob(r, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("job "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("").Wrap(err))
		return
	}
	// A job of a library the caller cannot read (or of no library: a
	// catalog job, admin only) is not found, like an unknown id.
	if !canSeeJob(principal.FromContext(r.Context()), j.LibraryID) {
		httperror.Write(w, r, httperror.NotFound("job "+id))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, j)
}

// canSeeJob: admins and all-library callers see every job; others only
// jobs of a library they were granted (never a library-less job).
func canSeeJob(p *principal.Principal, libraryID *string) bool {
	if p == nil {
		return false
	}
	if p.IsAdmin || p.AccessAllLibraries {
		return true
	}
	return libraryID != nil && p.HasLibrary(*libraryID)
}

// List supports a thin filter set for the queue UI:
//
//	?stage=...&state=...&limit=...&cursor=...
//
// Newest first; `next` is the shared paginate cursor of the following
// page (null on the last one).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
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
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	conds := []string{}
	args := []any{}
	// Library scope: a caller without every library sees only the jobs of
	// the libraries granted to it (no library-less jobs).
	if !p.IsAdmin && !p.AccessAllLibraries {
		if len(p.Libraries) == 0 {
			common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": []Job{}, "next": nil})
			return
		}
		ph := make([]string, len(p.Libraries))
		for i, l := range p.Libraries {
			ph[i] = "$" + strconv.Itoa(len(args)+1)
			args = append(args, l)
		}
		conds = append(conds, "library_id IN ("+strings.Join(ph, ",")+")")
	}
	if v := r.URL.Query().Get("stage"); v != "" {
		conds = append(conds, "stage = $"+strconv.Itoa(len(args)+1))
		args = append(args, v)
	}
	if v := r.URL.Query().Get("state"); v != "" {
		conds = append(conds, "state = $"+strconv.Itoa(len(args)+1))
		args = append(args, v)
	}
	// Keyset paging through the shared cursor (Story 7.2): ?cursor= is
	// the `next` of the previous page.
	cur, ce := paginate.Decode(r.URL.Query().Get("cursor"))
	if ce != nil {
		httperror.Write(w, r, ce)
		return
	}
	if frag, cargs := paginate.Where(paginate.SortSpec{TimeCol: "created_at", IDCol: "id", Desc: true}, cur, len(args)+1, paginate.IDKindBigint); frag != "" {
		conds = append(conds, frag)
		args = append(args, cargs...)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	args = append(args, limit+1) // one more row tells whether another page exists
	q := `SELECT ` + jobColumns + `
	      FROM processing_jobs` + where + ` ORDER BY created_at DESC, id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := h.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list jobs").Wrap(err))
		return
	}
	defer rows.Close()
	items := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			continue
		}
		items = append(items, j)
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		enc := paginate.Encode(paginate.Cursor{Updated: last.CreatedAt, ID: last.ID})
		next = &enc
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items, "next": next})
}

// forceTooEarly refuses a force pause inside the pause grace: the job's
// pause must be requested and have waited pause.grace_sec (updated_at is when
// it was asked for). Nil when the grace is unset, passed, or the job is gone.
func (h *Handler) forceTooEarly(r *http.Request, id string, now time.Time) *httperror.Error {
	grace, ok := h.Settings.Seconds(r.Context(), runtimecfg.PauseGrace)
	if !ok {
		return nil
	}
	var requested bool
	var at sql.NullTime
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT pause_requested, updated_at FROM processing_jobs WHERE id = $1`, id).Scan(&requested, &at)
	if err != nil {
		return nil // no such job: the update and load below answer
	}
	if !requested {
		return &httperror.Error{
			Type:   "https://maktaba.tv/problems/pause-not-requested",
			Title:  "pause not requested",
			Status: http.StatusConflict,
			Detail: "request a pause first: a force pause is taken once the pause has waited " +
				strconv.Itoa(int(grace.Seconds())) + " s",
		}
	}
	if at.Valid {
		if wait := grace - now.Sub(at.Time); wait > 0 {
			return &httperror.Error{
				Type:   "https://maktaba.tv/problems/pause-grace",
				Title:  "pause grace not over",
				Status: http.StatusConflict,
				Detail: "the worker may still pause at its next segment: force pause is taken in " +
					strconv.Itoa(int(wait.Round(time.Second).Seconds())) + " s",
			}
		}
	}
	return nil
}

// Pause sets pause_requested. With “?force=true“ it shortcuts the
// state directly per AC-2.
func (h *Handler) Pause(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	force, _ := common.QueryBool(r, "force", false)
	now := h.now()
	var err error
	if force {
		if e := h.forceTooEarly(r, id, now); e != nil {
			httperror.Write(w, r, e)
			return
		}
		_, err = h.DB.ExecContext(r.Context(), `
			UPDATE processing_jobs SET state='paused', paused_reason='user-force',
			       claimed_by=NULL, pause_requested=FALSE, updated_at=$2
			WHERE id=$1 AND state IN ('running','pending')
		`, id, now)
	} else {
		_, err = h.DB.ExecContext(r.Context(), `
			UPDATE processing_jobs SET pause_requested=TRUE, updated_at=$2
			WHERE id=$1 AND state IN ('running','pending')
		`, id, now)
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("pause").Wrap(err))
		return
	}
	j, err := h.loadJob(r, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("job "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("").Wrap(err))
		return
	}
	if isTerminal(j.State) {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.tv/problems/job-terminal",
			Title:  "job already terminal",
			Status: http.StatusConflict,
			Detail: "cannot pause job in state " + j.State,
		})
		return
	}
	common.WriteJSON(w, r, http.StatusOK, j)
}

// Resume clears the paused_reason; the worker re-claims the row.
func (h *Handler) Resume(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	_, err := h.DB.ExecContext(r.Context(), `
		UPDATE processing_jobs SET paused_reason=NULL, pause_requested=FALSE,
		       state='pending', not_before=$2, updated_at=$2
		WHERE id=$1 AND state='paused'
	`, id, h.now())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("resume").Wrap(err))
		return
	}
	j, err := h.loadJob(r, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("job "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, j)
}

// Cancel sets cancel_requested.
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	_, err := h.DB.ExecContext(r.Context(), `
		UPDATE processing_jobs SET cancel_requested=TRUE, updated_at=$2
		WHERE id=$1 AND state NOT IN ('done','failed','cancelled','superseded')
	`, id, h.now())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("cancel").Wrap(err))
		return
	}
	j, err := h.loadJob(r, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("job "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, j)
}

// Retry resets a failed job. Non-failed → 409 (AC-5).
func (h *Handler) Retry(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	j, err := h.loadJob(r, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("job "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("").Wrap(err))
		return
	}
	if j.State != "failed" {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.tv/problems/job-not-failed",
			Title:  "job not failed",
			Status: http.StatusConflict,
			Detail: "retry only valid for failed jobs",
		})
		return
	}
	// Retry never updates the failed row in place: it re-enqueues the
	// subject through the same SQL function Python uses (forced), so the
	// failed row stays as history and the two languages cannot disagree
	// on coalescing (plan-06-11 section 4.2).
	// The retry asks for what the failed job was asked: its payload (a dry run
	// stays a dry run, an approval stays the same approval) and, for an Epic 47
	// stage, its place in the queue (the pipeline's follow-ups keep it too).
	var payload map[string]any
	var raw []byte
	err = h.DB.QueryRowContext(r.Context(), `SELECT payload FROM processing_jobs j WHERE j.id = $1`, id).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.Internal("retry").Wrap(err))
		return
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	var prio *int
	if _, ok := queues.Epic47Priorities[j.Stage]; ok {
		p := j.Priority
		prio = &p
	}
	res, err := queues.Enqueue(r.Context(), h.DB, queues.EnqueueParams{
		SubjectKind: j.SubjectKind, SubjectID: j.SubjectID, Stage: j.Stage, Force: true, Priority: prio,
		Reason:  queues.ReasonFor(r.Context(), "retry", map[string]any{"of_job": id}),
		Payload: payload,
	})
	if err != nil {
		httperror.Write(w, r, httperror.Internal("retry").Wrap(err))
		return
	}
	if !res.Accepted() || res.JobID == nil {
		httperror.Write(w, r, httperror.Conflict("", "retry not accepted: "+res.Outcome))
		return
	}
	j2, err := h.loadJob(r, strconv.FormatInt(*res.JobID, 10))
	if err != nil {
		httperror.Write(w, r, httperror.Internal("retry").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, j2)
}

// PauseVideo etc. — per-video aggregates (AC-6).
func (h *Handler) PauseVideo(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	res, err := h.DB.ExecContext(r.Context(), `
		UPDATE processing_jobs SET pause_requested=TRUE, updated_at=$2
		WHERE video_id=$1 AND state IN ('running','pending')
	`, id, h.now())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("pause video").Wrap(err))
		return
	}
	n, _ := res.RowsAffected()
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"affected": n})
}

func (h *Handler) ResumeVideo(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	res, err := h.DB.ExecContext(r.Context(), `
		UPDATE processing_jobs SET paused_reason=NULL, pause_requested=FALSE,
		       state='pending', not_before=$2, updated_at=$2
		WHERE video_id=$1 AND state='paused'
	`, id, h.now())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("resume video").Wrap(err))
		return
	}
	n, _ := res.RowsAffected()
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"affected": n})
}

func (h *Handler) CancelVideo(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	res, err := h.DB.ExecContext(r.Context(), `
		UPDATE processing_jobs SET cancel_requested=TRUE, updated_at=$2
		WHERE video_id=$1 AND state NOT IN ('done','failed','cancelled','superseded')
	`, id, h.now())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("cancel video").Wrap(err))
		return
	}
	n, _ := res.RowsAffected()
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"affected": n})
}

// StatsResponse is the AC-1 shape for /api/queue/stats.
type StatsResponse struct {
	ByStage             map[string]StageStats `json:"by_stage"`
	EtaSec              float64               `json:"eta_sec"`
	TotalInFlight       int                   `json:"total_in_flight"`
	OldestPendingAgeSec float64               `json:"oldest_pending_age_sec"`
	Workers             []WorkerInfo          `json:"workers"`
}

// StageStats is per-stage counts.
type StageStats struct {
	Pending int `json:"pending"`
	Running int `json:"running"`
	Paused  int `json:"paused"`
	Failed  int `json:"failed"`
	Done24h int `json:"done_24h"`
	// Extension is the optional extension that owns the stage (nil = core).
	Extension *string `json:"extension"`
	// ServedWorkers / ServedHosts: online workers that serve the stage.
	// NoWorker is true when the stage is enabled but nothing serves it,
	// so the UI can say "waiting for a worker that serves transcribe".
	ServedWorkers int      `json:"served_workers"`
	ServedHosts   []string `json:"served_hosts"`
	NoWorker      bool     `json:"no_worker"`

	ready int
	rate  int
}

// WorkerInfo is the AC-1 workers[] shape.
type WorkerInfo struct {
	ID            string     `json:"id"`
	Host          string     `json:"host"`
	LastHeartbeat *time.Time `json:"last_heartbeat,omitempty"`
	CurrentJobID  *string    `json:"current_job_id,omitempty"`
}

// rateWindowMin is the window of the throughput estimate behind eta_sec.
const rateWindowMin = 10

// workerHost extracts the host from a claimed_by value shaped
// "host/pid/uuid"; anything else is returned unchanged.
func workerHost(claimedBy string) string {
	if i := strings.IndexByte(claimedBy, '/'); i > 0 {
		return claimedBy[:i]
	}
	return claimedBy
}

// etaSeconds is the sum over stages of ready / rate, using only stages
// that completed work in the window (a stage with no rate has no
// estimate and contributes nothing).
func etaSeconds(stages map[string]StageStats) float64 {
	var total float64
	for _, st := range stages {
		if st.rate > 0 && st.ready > 0 {
			perSec := float64(st.rate) / (rateWindowMin * 60)
			total += float64(st.ready) / perSec
		}
	}
	return total
}

// Stats implements 7.13. Stage names come from job_stages (so new and
// extension stages appear without code changes); stages that only show
// up in processing_jobs are still reported.
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	ctx := r.Context()
	now := h.now()
	resp := StatsResponse{ByStage: map[string]StageStats{}, Workers: []WorkerInfo{}}

	enabled := map[string]bool{}
	if srows, err := h.DB.QueryContext(ctx, `SELECT stage, extension, enabled FROM job_stages`); err == nil {
		for srows.Next() {
			var stage string
			var ext sql.NullString
			var en bool
			if srows.Scan(&stage, &ext, &en) == nil {
				st := StageStats{ServedHosts: []string{}}
				if ext.Valid {
					v := ext.String
					st.Extension = &v
				}
				resp.ByStage[stage] = st
				enabled[stage] = en
			}
		}
		srows.Close()
	}

	rows, err := h.DB.QueryContext(ctx, `
		SELECT stage, state, COUNT(*) FROM processing_jobs GROUP BY stage, state
	`)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("stats").Wrap(err))
		return
	}
	defer rows.Close()
	for rows.Next() {
		var stage, state string
		var cnt int
		if err := rows.Scan(&stage, &state, &cnt); err != nil {
			continue
		}
		st := resp.ByStage[stage]
		if st.ServedHosts == nil {
			st.ServedHosts = []string{}
		}
		switch state {
		case "pending":
			st.Pending += cnt
		case "claimed", "running", "resuming":
			st.Running += cnt
			resp.TotalInFlight += cnt
		case "paused":
			st.Paused += cnt
		case "failed":
			st.Failed += cnt
		}
		resp.ByStage[stage] = st
	}

	// done_24h counts successful completions only.
	rows2, err := h.DB.QueryContext(ctx, `
		SELECT stage, COUNT(*) FROM processing_jobs
		WHERE state = 'done' AND finished_at > $1
		GROUP BY stage
	`, now.Add(-24*time.Hour))
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var stage string
			var cnt int
			if err := rows2.Scan(&stage, &cnt); err == nil {
				st := resp.ByStage[stage]
				st.Done24h = cnt
				resp.ByStage[stage] = st
			}
		}
	}

	// Throughput and backlog for the ETA.
	rows3, err := h.DB.QueryContext(ctx, `
		SELECT stage,
		       COUNT(*) FILTER (WHERE state = 'done' AND outcome IS DISTINCT FROM 'skipped_unchanged' AND finished_at > $2),
		       COUNT(*) FILTER (WHERE state = 'pending' AND (not_before IS NULL OR not_before <= $1))
		  FROM processing_jobs
		 WHERE state = 'pending' OR (state = 'done' AND finished_at > $2)
		 GROUP BY stage
	`, now, now.Add(-rateWindowMin*time.Minute))
	if err == nil {
		defer rows3.Close()
		for rows3.Next() {
			var stage string
			var rate, ready int
			if err := rows3.Scan(&stage, &rate, &ready); err == nil {
				st := resp.ByStage[stage]
				st.rate, st.ready = rate, ready
				resp.ByStage[stage] = st
			}
		}
	}
	resp.EtaSec = etaSeconds(resp.ByStage)

	// oldest pending age (seconds).
	var oldest sql.NullTime
	if err := h.DB.QueryRowContext(ctx, `
		SELECT MIN(created_at) FROM processing_jobs WHERE state = 'pending'
	`).Scan(&oldest); err == nil && oldest.Valid {
		resp.OldestPendingAgeSec = now.Sub(oldest.Time).Seconds()
	}

	// Workers: one entry per distinct claimed_by, not one per job.
	wrows, werr := h.DB.QueryContext(ctx, `
		SELECT claimed_by, MAX(last_heartbeat_at), MIN(id)
		FROM processing_jobs
		WHERE state IN ('claimed','running','resuming') AND claimed_by IS NOT NULL
		GROUP BY claimed_by
		ORDER BY MAX(last_heartbeat_at) DESC
	`)
	if werr == nil {
		defer wrows.Close()
		for wrows.Next() {
			var wi WorkerInfo
			var hb sql.NullTime
			var jobID string
			var claimed string
			if err := wrows.Scan(&claimed, &hb, &jobID); err != nil {
				continue
			}
			wi.ID, wi.Host = claimed, workerHost(claimed)
			if hb.Valid {
				t := hb.Time
				wi.LastHeartbeat = &t
			}
			wi.CurrentJobID = &jobID
			resp.Workers = append(resp.Workers, wi)
		}
	}

	// Registry coverage (best effort: the workers table may not exist
	// yet on an older schema, in which case no_worker stays false).
	if reg, err := queues.LoadWorkers(ctx, h.DB, now); err == nil {
		served := queues.ServedByStage(reg)
		for stage, st := range resp.ByStage {
			sb := served[stage]
			st.ServedWorkers = sb.Workers
			if sb.Hosts != nil {
				st.ServedHosts = sb.Hosts
			}
			st.NoWorker = enabled[stage] && st.Pending > 0 && sb.Workers == 0
			resp.ByStage[stage] = st
		}
	}

	common.WriteJSON(w, r, http.StatusOK, resp)
}

// loadJob reads a single row.
func (h *Handler) loadJob(r *http.Request, id string) (Job, error) {
	row := h.DB.QueryRowContext(r.Context(), `
		SELECT `+jobColumns+`
		FROM processing_jobs WHERE id = $1
	`, id)
	return scanJob(row)
}

type rowScanner interface {
	Scan(...any) error
}

func scanJob(rs rowScanner) (Job, error) {
	var j Job
	var videoID, libraryID, claimedBy sql.NullString
	var errStr, subjKind, subjID sql.NullString
	var est sql.NullFloat64
	var notBefore, hb sql.NullTime
	if err := rs.Scan(&j.ID, &videoID, &libraryID, &j.Stage, &j.State, &j.Priority,
		&j.Attempts, &j.PauseRequested, &j.CancelRequested, &j.CreatedAt,
		&notBefore, &claimedBy, &hb, &errStr, &est, &subjKind, &subjID); err != nil {
		return Job{}, err
	}
	if videoID.Valid {
		j.VideoID = &videoID.String
	}
	if libraryID.Valid {
		j.LibraryID = &libraryID.String
	}
	if claimedBy.Valid {
		j.ClaimedBy = &claimedBy.String
	}
	if notBefore.Valid {
		t := notBefore.Time
		j.NotBefore = &t
	}
	if hb.Valid {
		t := hb.Time
		j.LastHeartbeatAt = &t
	}
	if errStr.Valid {
		j.Error = &errStr.String
	}
	if est.Valid {
		v := est.Float64
		j.EstimatedRemainingSec = &v
	}
	j.SubjectKind, j.SubjectID = subjKind.String, subjID.String
	return j, nil
}

func isTerminal(s string) bool {
	switch s {
	case "done", "failed", "cancelled", "superseded":
		return true
	}
	return false
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}

func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return false
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return false
	}
	return true
}

// adminOnly refuses anyone but a home admin (the API enforces roles: these routes back the
// admin pages, and hiding the pages is not the rule).
func adminOnly(w http.ResponseWriter, r *http.Request) bool {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return false
	}
	return true
}
