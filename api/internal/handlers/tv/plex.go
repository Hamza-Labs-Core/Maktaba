package tv

// Plex Live TV (audit P0-1): the status of Maktaba's tuner in Plex and the
// two admin actions. The Plex calls themselves are the pipeline's
// (extensions/plex/livetv.py, stage plex_livetv); this side reads what the
// stage last saw (plex_livetv_state, slot 0116) and queues the stage.
//
//	GET  /api/tv/plex           (admin) status: Plex configured / reachable,
//	                            DVRs and tuners found, Maktaba's tuner
//	                            registered?, channels mapped, last guide
//	                            reload and channel-map push, errors
//	POST /api/tv/plex/refresh   (admin) queue plex_livetv now (forced: the
//	                            channel map is pushed and the guide reloaded)
//	POST /api/tv/plex/register  (admin) ask the stage to add the tuner to the
//	                            DVR Plex's settings page shows, as another
//	                            channel source with the XMLTV guide. 409 when
//	                            the last check found it there already, or the
//	                            tuner is off. The stage checks again and
//	                            refuses the same way (idempotent).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// PlexStage is the pipeline stage that talks to Plex Live TV.
const PlexStage = "plex_livetv"

// catalogSubject is the catalog subject id (engine/graph.py CATALOG_ID).
const catalogSubject = "00000000-0000-0000-0000-00000000ca7a"

// tsLayout matches the pipeline's timestamp text (engine/sql.py to_ts), so
// both dialects read it back.
const tsLayout = "2006-01-02T15:04:05.000Z"

// EnqueueFunc queues a stage for the catalog subject; returns the outcome
// (inserted, coalesced, rerun_requested, disabled, unknown_stage, ...).
type EnqueueFunc func(ctx context.Context, stage string, force bool, reason map[string]any) (string, *int64, error)

// PlexStatus is the GET /api/tv/plex body.
type PlexStatus struct {
	Plex                PlexExtension  `json:"plex"`
	Stage               *StageState    `json:"stage"`
	Job                 *JobState      `json:"job"`
	CheckedAt           *string        `json:"checked_at"`
	GuideReloadedAt     *string        `json:"guide_reloaded_at"`
	ChannelMapAt        *string        `json:"channelmap_at"`
	LastError           *string        `json:"last_error"`
	LastErrorAt         *string        `json:"last_error_at"`
	RegisterRequestedAt *string        `json:"register_requested_at"`
	Status              map[string]any `json:"status"`
}

// PlexExtension is the Plex extension's state as far as Live TV cares.
type PlexExtension struct {
	Enabled    bool   `json:"enabled"`
	Configured bool   `json:"configured"`
	URL        string `json:"url"`
}

// StageState is plex_livetv's job_stages row.
type StageState struct {
	Enabled        bool    `json:"enabled"`
	DisabledReason *string `json:"disabled_reason"`
}

// JobState is the latest plex_livetv job.
type JobState struct {
	ID         int64   `json:"id"`
	State      string  `json:"state"`
	CreatedAt  *string `json:"created_at"`
	FinishedAt *string `json:"finished_at"`
	Paused     *string `json:"paused_reason"`
}

// PlexAction is the POST refresh / register body.
type PlexAction struct {
	Queued  bool   `json:"queued"`
	Outcome string `json:"outcome"`
	JobID   *int64 `json:"job_id,omitempty"`
}

func (h *Handler) mountPlex(r chi.Router) {
	r.Get("/api/tv/plex", h.PlexStatus)
	r.Post("/api/tv/plex/refresh", h.PlexRefresh)
	r.Post("/api/tv/plex/register", h.PlexRegister)
}

func (h *Handler) enqueue(ctx context.Context, force bool, reason map[string]any) (string, *int64, error) {
	if h.Enqueue != nil {
		return h.Enqueue(ctx, PlexStage, force, reason)
	}
	res, err := queues.Enqueue(ctx, h.DB, queues.EnqueueParams{
		SubjectKind: "catalog", SubjectID: catalogSubject, Stage: PlexStage,
		Force: force, Reason: reason,
	})
	return res.Outcome, res.JobID, err
}

// NotifyPlex queues a (non-forced) plex_livetv run: the lineup changed by
// hand (channels CRUD, the tuner settings). Best-effort.
func (h *Handler) NotifyPlex(ctx context.Context) {
	if h.DB == nil {
		return
	}
	_, _, _ = h.enqueue(ctx, false, queues.ReasonFor(ctx, "lineup_edited", nil))
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now()
}

// PlexStatus answers GET /api/tv/plex.
func (h *Handler) PlexStatus(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	out, err := h.plexStatus(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load plex live tv status"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Handler) plexStatus(ctx context.Context) (PlexStatus, error) {
	out := PlexStatus{Status: map[string]any{}, Plex: h.plexExtension(ctx)}
	var checked, reloaded, mapped, lastErr, lastErrAt, asked sql.NullString
	var raw []byte
	err := h.DB.QueryRowContext(ctx, `
		SELECT status, checked_at, guide_reloaded_at, channelmap_at, last_error,
		       last_error_at, register_requested_at
		FROM plex_livetv_state WHERE id = 1`).Scan(
		&raw, &checked, &reloaded, &mapped, &lastErr, &lastErrAt, &asked)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return out, err
	default:
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out.Status)
			if out.Status == nil {
				out.Status = map[string]any{}
			}
		}
		out.CheckedAt, out.GuideReloadedAt, out.ChannelMapAt = ptr(checked), ptr(reloaded), ptr(mapped)
		out.LastError, out.LastErrorAt, out.RegisterRequestedAt = ptr(lastErr), ptr(lastErrAt), ptr(asked)
	}
	// best-effort extras: a database without the queue tables still answers
	var st StageState
	var reason sql.NullString
	if err := h.DB.QueryRowContext(ctx,
		`SELECT enabled, disabled_reason FROM job_stages WHERE stage = $1`, PlexStage,
	).Scan(&st.Enabled, &reason); err == nil {
		st.DisabledReason = ptr(reason)
		out.Stage = &st
	}
	var j JobState
	var created, finished, paused sql.NullString
	if err := h.DB.QueryRowContext(ctx, `
		SELECT id, state, created_at, finished_at, paused_reason FROM processing_jobs
		WHERE stage = $1 ORDER BY id DESC LIMIT 1`, PlexStage,
	).Scan(&j.ID, &j.State, &created, &finished, &paused); err == nil {
		j.CreatedAt, j.FinishedAt, j.Paused = ptr(created), ptr(finished), ptr(paused)
		out.Job = &j
	}
	return out, nil
}

func (h *Handler) plexExtension(ctx context.Context) PlexExtension {
	var e PlexExtension
	var raw []byte
	if err := h.DB.QueryRowContext(ctx,
		`SELECT enabled, settings FROM extensions WHERE name = 'plex'`,
	).Scan(&e.Enabled, &raw); err != nil {
		return e
	}
	settings := map[string]any{}
	_ = json.Unmarshal(raw, &settings)
	str := func(k string) string {
		s, _ := settings[k].(string)
		return strings.TrimSpace(s)
	}
	e.URL = str("url") // never the token
	tokenSet := str("token") != ""
	if !tokenSet && h.Secrets != nil {
		tokenSet, _ = h.Secrets.Has(ctx, secrets.ExtensionName("plex", "token"))
	}
	e.Configured = e.URL != "" && tokenSet
	return e
}

func ptr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

// PlexRefresh answers POST /api/tv/plex/refresh.
func (h *Handler) PlexRefresh(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	if e := h.plexExtension(r.Context()); !e.Enabled || !e.Configured {
		httperror.Write(w, r, httperror.Conflict("https://maktaba.dev/problems/plex-not-configured",
			"turn on the Plex extension and sign in with Plex first"))
		return
	}
	h.queuePlex(w, r, "plex_refresh")
}

// PlexRegister answers POST /api/tv/plex/register.
func (h *Handler) PlexRegister(w http.ResponseWriter, r *http.Request) {
	if !isAdmin(r) {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	ctx := r.Context()
	if e := h.plexExtension(ctx); !e.Enabled || !e.Configured {
		httperror.Write(w, r, httperror.Conflict("https://maktaba.dev/problems/plex-not-configured",
			"turn on the Plex extension and sign in with Plex first"))
		return
	}
	d, err := h.device(ctx)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load hdhr device"))
		return
	}
	if !d.Enabled {
		httperror.Write(w, r, httperror.Conflict("https://maktaba.dev/problems/tuner-off",
			"turn the HDHomeRun tuner on first"))
		return
	}
	cur, err := h.plexStatus(ctx)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load plex live tv status"))
		return
	}
	if mk, ok := cur.Status["maktaba"].(map[string]any); ok {
		if reg, _ := mk["registered"].(bool); reg {
			httperror.Write(w, r, httperror.Conflict("https://maktaba.dev/problems/plex-already-registered",
				"Plex already has a DVR with Maktaba's tuner"))
			return
		}
	}
	at := h.now().UTC().Format(tsLayout)
	if _, err := h.DB.ExecContext(ctx, `
		INSERT INTO plex_livetv_state (id, register_requested_at, requested_base_url, updated_at)
		VALUES (1, $1, $2, $1)
		ON CONFLICT (id) DO UPDATE SET register_requested_at = EXCLUDED.register_requested_at,
		    requested_base_url = EXCLUDED.requested_base_url, updated_at = EXCLUDED.updated_at
	`, at, h.info(r).StreamBaseURL); err != nil {
		httperror.Write(w, r, httperror.Internal("save register request"))
		return
	}
	h.queuePlex(w, r, "plex_register")
}

func (h *Handler) queuePlex(w http.ResponseWriter, r *http.Request, why string) {
	outcome, id, err := h.enqueue(r.Context(), true, queues.ReasonFor(r.Context(), why, nil))
	if err != nil {
		httperror.Write(w, r, httperror.Internal("queue plex live tv"))
		return
	}
	switch outcome {
	case queues.OutcomeInserted, queues.OutcomeCoalesced, queues.OutcomeRerunRequested:
		common.WriteJSON(w, r, http.StatusAccepted, PlexAction{Queued: true, Outcome: outcome, JobID: id})
	default: // disabled / unknown_stage: no worker has synced the stage, or it is off
		httperror.Write(w, r, httperror.Conflict("https://maktaba.dev/problems/plex-stage-off",
			"the Plex Live TV stage is not available ("+outcome+"): start a worker with the Plex extension on"))
	}
}
