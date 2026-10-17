package queues

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// cooperativePauseStages are the stages whose handlers honour a
// pause_requested flag mid-run (plan-06-11 D10: only transcribe today).
// "?interrupt=true" is a no-op for every other stage because killing a
// seconds-long job mid-write gains nothing.
var cooperativePauseStages = map[string]bool{"transcribe": true}

// PauseStage implements POST /api/queues/{stage}/pause?interrupt=false.
func (h *Handler) PauseStage(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	stage := chi.URLParam(r, "stage")
	interrupt, e := common.QueryBool(r, "interrupt", false)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	now := h.now()
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE job_stages SET paused = TRUE, paused_at = $2, paused_by = $3, updated_at = $2 WHERE stage = $1`,
		stage, now, actor(r))
	if err != nil {
		writeErr(w, r, "pause stage", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httperror.Write(w, r, httperror.NotFound("queue "+stage))
		return
	}
	if interrupt && cooperativePauseStages[stage] {
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE processing_jobs SET pause_requested = TRUE, updated_at = $2
			WHERE stage = $1 AND state IN `+inFlightSQL, stage, now); err != nil {
			writeErr(w, r, "interrupt stage", err)
			return
		}
	}
	h.publish(r.Context(), "queue.control", map[string]any{"stage": stage, "paused": true, "by": actor(r)})
	common.WriteNoContent(w)
}

// ResumeStage implements POST /api/queues/{stage}/resume. It also
// clears pause_requested on the stage's rows so jobs interrupted by an
// earlier "pause + interrupt" become claimable again.
func (h *Handler) ResumeStage(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	stage := chi.URLParam(r, "stage")
	now := h.now()
	res, err := h.DB.ExecContext(r.Context(),
		`UPDATE job_stages SET paused = FALSE, paused_at = NULL, paused_by = NULL, updated_at = $2 WHERE stage = $1`,
		stage, now)
	if err != nil {
		writeErr(w, r, "resume stage", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httperror.Write(w, r, httperror.NotFound("queue "+stage))
		return
	}
	if cooperativePauseStages[stage] {
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE processing_jobs SET pause_requested = FALSE, updated_at = $2
			WHERE stage = $1 AND pause_requested AND state IN `+liveStatesSQL, stage, now); err != nil {
			writeErr(w, r, "resume stage", err)
			return
		}
	}
	h.publish(r.Context(), "queue.control", map[string]any{"stage": stage, "paused": false, "by": actor(r)})
	common.WriteNoContent(w)
}

const setPausedAllSQL = `
INSERT INTO queue_control (id, paused, paused_at, paused_by, updated_at)
VALUES (1, $1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE
   SET paused = EXCLUDED.paused, paused_at = EXCLUDED.paused_at,
       paused_by = EXCLUDED.paused_by, updated_at = EXCLUDED.updated_at`

func (h *Handler) setPausedAll(w http.ResponseWriter, r *http.Request, paused bool) {
	if !requireAdmin(w, r) {
		return
	}
	now := h.now()
	var at, by any
	if paused {
		at, by = now, actor(r)
	}
	if _, err := h.DB.ExecContext(r.Context(), setPausedAllSQL, paused, at, by, now); err != nil {
		writeErr(w, r, "pause all", err)
		return
	}
	h.publish(r.Context(), "queue.control", map[string]any{"stage": nil, "paused": paused, "by": actor(r)})
	common.WriteNoContent(w)
}

// PauseAll implements POST /api/queues/pause-all.
func (h *Handler) PauseAll(w http.ResponseWriter, r *http.Request) { h.setPausedAll(w, r, true) }

// ResumeAll implements POST /api/queues/resume-all.
func (h *Handler) ResumeAll(w http.ResponseWriter, r *http.Request) { h.setPausedAll(w, r, false) }

type patchStageBody struct {
	Enabled   *bool   `json:"enabled"`
	WriteMode *string `json:"write_mode"`
}

// PatchStage implements PATCH /api/queues/{stage}: operator switches.
// A stage disabled because it is not configured cannot be enabled by
// the operator (409), and write_mode only exists on external-write
// stages (400).
func (h *Handler) PatchStage(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	stage := chi.URLParam(r, "stage")
	var body patchStageBody
	if e := common.ReadJSON(r, &body, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if body.Enabled == nil && body.WriteMode == nil {
		httperror.Write(w, r, httperror.BadRequest("nothing to change: send enabled and/or write_mode"))
		return
	}
	if body.WriteMode != nil && *body.WriteMode != "auto" && *body.WriteMode != "review" {
		httperror.Write(w, r, httperror.BadRequest(`write_mode must be "auto" or "review"`))
		return
	}
	cur, ok := h.stageOr404(w, r, stage)
	if !ok {
		return
	}
	if body.WriteMode != nil && !cur.ExternalWrite {
		httperror.Write(w, r, httperror.BadRequest("stage "+stage+" does not write to an external system"))
		return
	}
	if body.Enabled != nil && *body.Enabled && !cur.Enabled &&
		cur.DisabledReason != nil && *cur.DisabledReason == "not_configured" {
		httperror.Write(w, r, httperror.Conflict("", "stage "+stage+" is not configured; set up its integration first"))
		return
	}
	var enabled, mode any
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	if body.WriteMode != nil {
		mode = *body.WriteMode
	}
	if _, err := h.DB.ExecContext(r.Context(), `
		UPDATE job_stages
		   SET enabled = COALESCE($2::boolean, enabled),
		       disabled_reason = CASE WHEN $2::boolean IS NULL THEN disabled_reason
		                              WHEN $2::boolean THEN NULL ELSE 'operator' END,
		       write_mode = COALESCE($3::text, write_mode),
		       updated_at = $4
		 WHERE stage = $1`, stage, enabled, mode, h.now()); err != nil {
		writeErr(w, r, "update stage", err)
		return
	}
	out, ok := h.stageOr404(w, r, stage)
	if !ok {
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// ---------- retry / apply ----------

type retryBody struct {
	Cause  string  `json:"cause"`
	JobIDs []int64 `json:"job_ids"`
}

type applyBody struct {
	JobIDs []int64 `json:"job_ids"`
	All    bool    `json:"all"`
}

// retryByCausePriority: bulk retries of a sick dependency (e.g. NAS
// timeouts) must not starve normal work (plan-06-11 §15).
const retryByCausePriority = 200

const maxBulkRetry = 20000

// failedForRetrySQL: latest-failed rows of a stage whose coded cause
// equals $2, plus uncoded legacy rows (filtered in Go by legacyCause).
const failedForRetrySQL = latestFailedCTE + `
SELECT id, subject_kind, subject_id::text, error_cause, error
  FROM latest
 WHERE state = 'failed' AND (error_cause = $2::text OR error_cause IS NULL)
 ORDER BY id
 LIMIT ` + "20000"

// failedByIDsSQL: the listed rows, each only while it is still the latest
// row of its subject and stage (a failure a later run has superseded is
// history, not something to redo - with force, for external writes).
const failedByIDsSQL = `
SELECT j.id, j.subject_kind, j.subject_id::text, j.state
  FROM processing_jobs j
 WHERE j.stage = $1 AND j.id = ANY($2::bigint[])
   AND NOT EXISTS (SELECT 1 FROM processing_jobs n
                    WHERE n.subject_kind = j.subject_kind AND n.subject_id = j.subject_id
                      AND n.stage = j.stage AND n.id > j.id)`

const reviewRowsSQL = `
SELECT j.id, j.subject_kind, j.subject_id::text,
       COALESCE((j.output_summary -> 'review_digests')::text, '[]')
  FROM processing_jobs j
 WHERE j.stage = $1 AND j.state = 'done' AND j.outcome = 'awaiting_review'
   AND ($2::boolean OR j.id = ANY($3::bigint[]))
   AND NOT EXISTS (SELECT 1 FROM processing_jobs n
                    WHERE n.subject_kind = j.subject_kind AND n.subject_id = j.subject_id
                      AND n.stage = j.stage AND n.id > j.id)
 ORDER BY j.id
 LIMIT ` + "20000"

type target struct {
	jobID int64
	kind  string
	id    string
	hash  string
}

// Retry implements POST /api/queues/{stage}/retry {cause?, job_ids?}.
// Each target is re-enqueued with force through the SQL function; the
// failed rows stay as history but stop being the latest row, so they
// leave the open issues.
func (h *Handler) Retry(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	stage := chi.URLParam(r, "stage")
	var body retryBody
	if e := common.ReadJSON(r, &body, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if body.Cause == "" && len(body.JobIDs) == 0 {
		httperror.Write(w, r, httperror.BadRequest("one of cause or job_ids is required"))
		return
	}
	if len(body.JobIDs) > maxBulkRetry {
		httperror.Write(w, r, httperror.BadRequest("too many job_ids"))
		return
	}
	if _, ok := h.stageOr404(w, r, stage); !ok {
		return
	}

	var targets []target
	var prio *int
	reason := map[string]any{}
	if body.Cause != "" {
		p := retryByCausePriority
		prio = &p
		reason["cause"] = body.Cause
		rows, err := h.DB.QueryContext(r.Context(), failedForRetrySQL, stage, body.Cause)
		if err != nil {
			writeErr(w, r, "find failures", err)
			return
		}
		for rows.Next() {
			var t target
			var cause, errTxt sql.NullString
			if err := rows.Scan(&t.jobID, &t.kind, &t.id, &cause, &errTxt); err != nil {
				rows.Close()
				writeErr(w, r, "find failures", err)
				return
			}
			effective := cause.String
			if !cause.Valid {
				effective = legacyCause(errTxt.String)
			}
			if effective == body.Cause {
				targets = append(targets, t)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			writeErr(w, r, "find failures", err)
			return
		}
		rows.Close()
	} else {
		rows, err := h.DB.QueryContext(r.Context(), failedByIDsSQL, stage, pq.Array(body.JobIDs))
		if err != nil {
			writeErr(w, r, "find jobs", err)
			return
		}
		for rows.Next() {
			var t target
			var state string
			if err := rows.Scan(&t.jobID, &t.kind, &t.id, &state); err != nil {
				rows.Close()
				writeErr(w, r, "find jobs", err)
				return
			}
			if state == "failed" {
				targets = append(targets, t)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			writeErr(w, r, "find jobs", err)
			return
		}
		rows.Close()
	}

	enq, coal, err := h.enqueueTargets(r, stage, targets, EnqueueParams{
		Priority: prio, Force: true,
	}, "retry", reason)
	if err != nil {
		writeErr(w, r, "retry", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"enqueued": enq, "coalesced": coal})
}

// Apply implements POST /api/queues/{stage}/apply {job_ids?, all?}: the
// operator approves a reviewed write. The follow-up job is forced and
// carries {"apply": true, "plan_digests": [...]}: the digests of the
// change sets the reviewed run parked (output_summary.review_digests).
// The stage applies a change set only if its digest is one of them; a
// plan that changed since the review is parked again.
func (h *Handler) Apply(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	stage := chi.URLParam(r, "stage")
	var body applyBody
	if e := common.ReadJSON(r, &body, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if !body.All && len(body.JobIDs) == 0 {
		httperror.Write(w, r, httperror.BadRequest("one of all or job_ids is required"))
		return
	}
	info, ok := h.stageOr404(w, r, stage)
	if !ok {
		return
	}
	if !info.ExternalWrite {
		httperror.Write(w, r, httperror.Conflict("", "stage "+stage+" has no review step to apply"))
		return
	}
	ids := body.JobIDs
	if ids == nil {
		ids = []int64{}
	}
	rows, err := h.DB.QueryContext(r.Context(), reviewRowsSQL, stage, body.All, pq.Array(ids))
	if err != nil {
		writeErr(w, r, "find reviews", err)
		return
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.jobID, &t.kind, &t.id, &t.hash); err != nil {
			rows.Close()
			writeErr(w, r, "find reviews", err)
			return
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeErr(w, r, "find reviews", err)
		return
	}
	rows.Close()

	enq, _, err := h.enqueueTargets(r, stage, targets, EnqueueParams{Force: true}, "apply", map[string]any{}, withApplyPayload)
	if err != nil {
		writeErr(w, r, "apply", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"enqueued": enq})
}

type targetOpt func(t target, p *EnqueueParams)

func withApplyPayload(t target, p *EnqueueParams) {
	var digests []string
	if err := json.Unmarshal([]byte(t.hash), &digests); err != nil || digests == nil {
		digests = []string{}
	}
	p.Payload = map[string]any{"apply": true, "plan_digests": digests}
}

// mergeApprovalSQL adds an approval to the job the enqueue landed on. The
// enqueue function keeps an existing pending / running row's payload (it
// merges only "seasons"), so without this an Apply that coalesced would
// be dropped; a running row hands its payload to its follow-up.
const mergeApprovalSQL = `
UPDATE processing_jobs
   SET payload = COALESCE(payload, '{}'::jsonb)
       || jsonb_build_object('apply', true, 'plan_digests', (
            SELECT COALESCE(jsonb_agg(DISTINCT d), '[]'::jsonb)
              FROM jsonb_array_elements(
                     COALESCE(payload -> 'plan_digests', '[]'::jsonb) || $2::jsonb) AS d))
 WHERE id = $1`

// enqueueTargets enqueues every target in ONE transaction and tallies
// the outcomes: enqueued = inserted, coalesced = coalesced or
// rerun_requested. Targets whose stage is disabled / invalid are
// skipped silently (they are not an operator error).
func (h *Handler) enqueueTargets(r *http.Request, stage string, targets []target, base EnqueueParams,
	why string, extra map[string]any, opts ...targetOpt) (enqueued, coalesced int, err error) {
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, t := range targets {
		p := base
		p.SubjectKind, p.SubjectID, p.Stage = t.kind, t.id, stage
		ex := map[string]any{"of_job": t.jobID}
		for k, v := range extra {
			ex[k] = v
		}
		p.Reason = ReasonFor(ctx, why, ex)
		for _, o := range opts {
			o(t, &p)
		}
		res, err := Enqueue(ctx, tx, p)
		if err != nil {
			return 0, 0, err
		}
		if res.JobID != nil && p.Payload != nil && p.Payload["apply"] == true {
			digests, err := json.Marshal(p.Payload["plan_digests"])
			if err != nil {
				return 0, 0, err
			}
			if _, err := tx.ExecContext(ctx, mergeApprovalSQL, *res.JobID, string(digests)); err != nil {
				return 0, 0, err
			}
		}
		switch res.Outcome {
		case OutcomeInserted:
			enqueued++
		case OutcomeCoalesced, OutcomeRerunRequested:
			coalesced++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return enqueued, coalesced, nil
}
