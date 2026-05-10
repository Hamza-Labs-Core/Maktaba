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
// guard for ``retry`` rejects non-failed jobs with 409.
package jobs

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Job is the over-the-wire shape; minimal — handlers don't return rich
// objects, mostly status flags so the UI can re-render.
type Job struct {
	ID                    string    `json:"id"`
	VideoID               *string   `json:"video_id,omitempty"`
	LibraryID             *string   `json:"library_id,omitempty"`
	Stage                 string    `json:"stage"`
	State                 string    `json:"state"`
	Priority              int       `json:"priority"`
	Attempts              int       `json:"attempts"`
	PauseRequested        bool      `json:"pause_requested"`
	CancelRequested       bool      `json:"cancel_requested"`
	CreatedAt             time.Time `json:"created_at"`
	NotBefore             *time.Time `json:"not_before,omitempty"`
	ClaimedBy             *string   `json:"claimed_by,omitempty"`
	LastHeartbeatAt       *time.Time `json:"last_heartbeat_at,omitempty"`
	EstimatedRemainingSec *float64  `json:"estimated_remaining_sec,omitempty"`
	Error                 *string   `json:"error,omitempty"`
}

// Handler bundles deps.
type Handler struct {
	DB      *sql.DB
	NowFunc func() time.Time
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
		httperror.Write(w, r, httperror.Internal(err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, j)
}

// List supports a thin filter set for the queue UI:
//
//	?stage=...&state=...&limit=...
//
// Pagination is offset-free — last page returned when fewer than ``limit``
// rows are emitted. The intended power tool is the cursor variant
// owned by `paginate`, but the queue UI uses small windows so we keep
// this surface simple.
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
	conds := []string{}
	args := []any{}
	if v := r.URL.Query().Get("stage"); v != "" {
		conds = append(conds, "stage = $"+strconv.Itoa(len(args)+1))
		args = append(args, v)
	}
	if v := r.URL.Query().Get("state"); v != "" {
		conds = append(conds, "state = $"+strconv.Itoa(len(args)+1))
		args = append(args, v)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	args = append(args, limit)
	q := `SELECT id, video_id, library_id, stage, state, priority, attempts,
	             pause_requested, cancel_requested, created_at, not_before,
	             claimed_by, last_heartbeat_at
	      FROM processing_jobs` + where + ` ORDER BY created_at DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := h.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list jobs: "+err.Error()))
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
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// Pause sets pause_requested. With ``?force=true`` it shortcuts the
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
		httperror.Write(w, r, httperror.Internal("pause: "+err.Error()))
		return
	}
	j, err := h.loadJob(r, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("job "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal(err.Error()))
		return
	}
	if isTerminal(j.State) {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/job-terminal",
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
		httperror.Write(w, r, httperror.Internal("resume: "+err.Error()))
		return
	}
	j, err := h.loadJob(r, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("job "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal(err.Error()))
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
		httperror.Write(w, r, httperror.Internal("cancel: "+err.Error()))
		return
	}
	j, err := h.loadJob(r, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("job "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal(err.Error()))
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
		httperror.Write(w, r, httperror.Internal(err.Error()))
		return
	}
	if j.State != "failed" {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/job-not-failed",
			Title:  "job not failed",
			Status: http.StatusConflict,
			Detail: "retry only valid for failed jobs",
		})
		return
	}
	_, err = h.DB.ExecContext(r.Context(), `
		UPDATE processing_jobs SET state='pending', attempts=0, error=NULL,
		       not_before=$2, updated_at=$2
		WHERE id=$1
	`, id, h.now())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("retry: "+err.Error()))
		return
	}
	j2, _ := h.loadJob(r, id)
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
		httperror.Write(w, r, httperror.Internal("pause video: "+err.Error()))
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
		httperror.Write(w, r, httperror.Internal("resume video: "+err.Error()))
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
		httperror.Write(w, r, httperror.Internal("cancel video: "+err.Error()))
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
}

// WorkerInfo is the AC-1 workers[] shape.
type WorkerInfo struct {
	ID            string     `json:"id"`
	Host          string     `json:"host"`
	LastHeartbeat *time.Time `json:"last_heartbeat,omitempty"`
	CurrentJobID  *string    `json:"current_job_id,omitempty"`
}

// Stats implements 7.13.
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	resp := StatsResponse{
		ByStage: map[string]StageStats{},
	}
	// Ensure all known stages appear (AC EC: zero rows still listed).
	for _, s := range []string{"scan", "probe", "audio_extract", "transcribe", "subtitle", "index", "embed", "rehash"} {
		resp.ByStage[s] = StageStats{}
	}

	rows, err := h.DB.QueryContext(r.Context(), `
		SELECT stage, state, COUNT(*) FROM processing_jobs GROUP BY stage, state
	`)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("stats: "+err.Error()))
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
		switch state {
		case "pending":
			st.Pending += cnt
		case "running":
			st.Running += cnt
			resp.TotalInFlight += cnt
		case "paused":
			st.Paused += cnt
		case "failed":
			st.Failed += cnt
		}
		resp.ByStage[stage] = st
	}

	// done_24h counts.
	rows2, err := h.DB.QueryContext(r.Context(), `
		SELECT stage, COUNT(*) FROM processing_jobs
		WHERE state IN ('done','failed') AND finished_at > $1
		GROUP BY stage
	`, h.now().Add(-24*time.Hour))
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

	// oldest pending age (seconds).
	var oldest sql.NullTime
	if err := h.DB.QueryRowContext(r.Context(), `
		SELECT MIN(created_at) FROM processing_jobs WHERE state = 'pending'
	`).Scan(&oldest); err == nil && oldest.Valid {
		resp.OldestPendingAgeSec = h.now().Sub(oldest.Time).Seconds()
	}

	// Worker snapshot.
	wrows, werr := h.DB.QueryContext(r.Context(), `
		SELECT claimed_by, last_heartbeat_at, id
		FROM processing_jobs
		WHERE state = 'running' AND claimed_by IS NOT NULL
		ORDER BY last_heartbeat_at DESC
	`)
	if werr == nil {
		defer wrows.Close()
		for wrows.Next() {
			var w WorkerInfo
			var hb sql.NullTime
			var jobID string
			var claimed sql.NullString
			if err := wrows.Scan(&claimed, &hb, &jobID); err != nil {
				continue
			}
			if claimed.Valid {
				w.ID = claimed.String
				w.Host = claimed.String
			}
			if hb.Valid {
				t := hb.Time
				w.LastHeartbeat = &t
			}
			w.CurrentJobID = &jobID
			resp.Workers = append(resp.Workers, w)
		}
	}

	common.WriteJSON(w, r, http.StatusOK, resp)
}

// loadJob reads a single row.
func (h *Handler) loadJob(r *http.Request, id string) (Job, error) {
	row := h.DB.QueryRowContext(r.Context(), `
		SELECT id, video_id, library_id, stage, state, priority, attempts,
		       pause_requested, cancel_requested, created_at, not_before,
		       claimed_by, last_heartbeat_at
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
	var errStr sql.NullString
	var notBefore, hb sql.NullTime
	if err := rs.Scan(&j.ID, &videoID, &libraryID, &j.Stage, &j.State, &j.Priority,
		&j.Attempts, &j.PauseRequested, &j.CancelRequested, &j.CreatedAt,
		&notBefore, &claimedBy, &hb); err != nil {
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
