package queues

// PUT /api/queues/{stage}/impl {impl}: where one of Maktaba's own stages runs (Epic 54,
// story 54.25 §3): "inproc" (its code in the pipeline worker) or "plugin" (the same code
// in its first-party plugin's runner). Admin only, audited, idempotent; the CLI's
// `maktaba-server stages impl <stage> <impl>` calls it.
//
// A flip never loses or reruns a job:
//
//  1. job_stages.flipping = true: the worker claims no new job of the stage;
//  2. the jobs in flight finish within the grace, else they are paused with their
//     checkpoints (state paused, paused_reason impl_flip: resuming one is no new attempt);
//  3. impl is set, flipping cleared, NOTIFY job_stages_changed;
//  4. claims resume. Queued jobs keep their rows, attempts, input hashes and history.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/firstparty"
)

// DefaultFlipGrace is how long a flip waits for the stage's running jobs (Epic 51 §4.5's
// drain grace for a stage group).
const DefaultFlipGrace = 60 * time.Second

// ImplState is what the route answers.
type ImplState struct {
	Stage  string  `json:"stage"`
	Impl   string  `json:"impl"`
	Plugin *string `json:"plugin"`
	// Paused counts the jobs a flip paused because they did not end within the grace.
	Paused int64 `json:"paused"`
}

func firstPartyOf(stage string) *string {
	if fp := firstparty.ByStage(stage); fp != nil {
		id := fp.ID
		return &id
	}
	return nil
}

// PutImpl flips a stage between in-process and its first-party plugin.
func (h *Handler) PutImpl(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	stage := chi.URLParam(r, "stage")
	var in struct {
		Impl string `json:"impl"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil ||
		(in.Impl != "inproc" && in.Impl != "plugin") {
		httperror.Write(w, r, httperror.BadRequest("impl is inproc or plugin"))
		return
	}
	plugin := firstPartyOf(stage)
	if plugin == nil {
		httperror.Write(w, r, httperror.Conflict("stage_not_first_party", stage+" is not a built-in plugin's stage"))
		return
	}
	ctx := r.Context()
	var cur string
	if err := h.DB.QueryRowContext(ctx, `SELECT impl FROM job_stages WHERE stage = $1`, stage).Scan(&cur); err != nil {
		httperror.Write(w, r, httperror.NotFound("queue "+stage))
		return
	}
	out := ImplState{Stage: stage, Impl: in.Impl, Plugin: plugin}
	if cur == in.Impl {
		common.WriteJSON(w, r, http.StatusOK, out) // idempotent: the current state
		return
	}
	paused, err := h.flip(ctx, stage, in.Impl, actor(r))
	if err != nil {
		writeErr(w, r, "flip stage", err)
		return
	}
	out.Paused = paused
	h.auditImpl(ctx, r, stage, cur, in.Impl, paused)
	h.publish(ctx, "queue.control", map[string]any{"stage": stage, "impl": in.Impl, "by": actor(r)})
	common.WriteJSON(w, r, http.StatusOK, out)
}

// auditImpl records a flip in audit_log (category queues, action impl).
func (h *Handler) auditImpl(ctx context.Context, r *http.Request, stage, from, to string, paused int64) {
	var userID any
	if p := principal.FromContext(r.Context()); p != nil && p.UserID != "" {
		userID = p.UserID
	}
	payload, _ := json.Marshal(map[string]any{"from": from, "to": to, "paused": paused})
	_, _ = h.DB.ExecContext(ctx, `INSERT INTO audit_log (category, action, actor_user_id, target_id, target_kind,
		actor_source, payload) VALUES ('queues', 'impl', $1, $2, 'stage', 'admin', $3)`, userID, stage, string(payload))
}

func (h *Handler) flipGrace() time.Duration {
	if h.FlipGrace > 0 {
		return h.FlipGrace
	}
	return DefaultFlipGrace
}

// flip is steps 1-4. A flip that fails part way clears flipping: claims resume on the old
// implementation.
func (h *Handler) flip(ctx context.Context, stage, impl, by string) (int64, error) {
	if _, err := h.DB.ExecContext(ctx, `UPDATE job_stages SET flipping = TRUE WHERE stage = $1`, stage); err != nil {
		return 0, err
	}
	done := false
	defer func() {
		if !done {
			_, _ = h.DB.ExecContext(context.WithoutCancel(ctx), `UPDATE job_stages SET flipping = FALSE WHERE stage = $1`, stage)
		}
	}()
	// a claim that read the stage row just before flipping was set finishes its claim
	// within the claim query's own transaction: wait one beat before counting
	sleep(ctx, 200*time.Millisecond)
	end := h.now().Add(h.flipGrace())
	for {
		var running int
		if err := h.DB.QueryRowContext(ctx, `SELECT count(*) FROM processing_jobs WHERE stage = $1
			AND state IN ('claimed', 'running', 'resuming')`, stage).Scan(&running); err != nil {
			return 0, err
		}
		if running == 0 || !h.now().Before(end) {
			break
		}
		if !sleep(ctx, 250*time.Millisecond) {
			return 0, ctx.Err()
		}
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	// what did not end in time is paused with its checkpoint: it runs again, no attempt spent
	rows, err := tx.QueryContext(ctx, `UPDATE processing_jobs SET state = 'paused', paused_reason = 'impl_flip',
		paused_at = now(), claimed_by = NULL WHERE stage = $1 AND state IN ('claimed', 'running', 'resuming') RETURNING id`, stage)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		_, _ = tx.ExecContext(ctx, `SELECT pg_notify('jobs.force_pause', $1)`, id)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE job_stages SET impl = $2, flipping = FALSE, impl_changed_at = now(),
		impl_changed_by = $3 WHERE stage = $1`, stage, impl, by); err != nil {
		return 0, err
	}
	_, _ = tx.ExecContext(ctx, `SELECT pg_notify('job_stages_changed', $1)`, stage)
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	done = true
	return int64(len(ids)), nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// fillImpl sets each stage's impl and first-party plugin (the Queues page's badge).
func (h *Handler) fillImpl(ctx context.Context, stages []StageInfo) {
	rows, err := h.DB.QueryContext(ctx, `SELECT stage, impl FROM job_stages`)
	if err != nil {
		return // a database before slot 0196: every stage is in-process
	}
	defer rows.Close()
	impl := map[string]string{}
	for rows.Next() {
		var s, i string
		if rows.Scan(&s, &i) == nil {
			impl[s] = i
		}
	}
	for i := range stages {
		stages[i].Impl = "inproc"
		if v := impl[stages[i].Stage]; strings.TrimSpace(v) != "" {
			stages[i].Impl = v
		}
		stages[i].Plugin = firstPartyOf(stages[i].Stage)
	}
}
