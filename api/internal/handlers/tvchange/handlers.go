// Package tvchange serves the TV change API (Epic 33, story 33.10 AC8; README "APIs"):
//
//	GET   /api/tv/changes?state=&limit=     list
//	POST  /api/tv/changes                   {title} a new empty draft (manual)
//	GET   /api/tv/changes/{id}              change + diff + verify_report + events
//	PATCH /api/tv/changes/{id}              {title?, swap_at?, swap_wait?}
//	POST  /api/tv/changes/{id}/stage        build the release and generation (tv_stage job)
//	POST  /api/tv/changes/{id}/verify       re-run the checks
//	POST  /api/tv/changes/{id}/approve      {swap_at?, ack_param_change?}
//	POST  /api/tv/changes/{id}/promote      swap now
//	POST  /api/tv/changes/{id}/cancel
//	POST  /api/tv/changes/{id}/preview      signed staging URLs (story 33.6)
//	POST  /api/tv/rollback                  501 until story 33.9
//	GET   /api/tv/slots                     the TV slots (one live process until 33.1)
//	GET   /api/tv/releases?limit=           releases with the change that made each
//
// Every route is admin-only; every response carries the change's allowed_actions; an
// invalid transition answers 409 with the current state.
package tvchange

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	core "github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
)

// Handler bundles the change store and the controller.
type Handler struct {
	DB         *sql.DB
	Store      *core.Store
	Controller *core.Controller
	// ApplyQueue posts the queue's Apply for a producer's reviewed digest (story 33.12
	// AC2); nil uses processing_jobs_enqueue.
	ApplyQueue func(ctx context.Context, stage, digest, changeID string) error
}

// New wires the handler over db and the controller.
func New(db *sql.DB, ctl *core.Controller) *Handler {
	h := &Handler{DB: db, Store: &core.Store{DB: db}, Controller: ctl}
	if ctl != nil && ctl.Store != nil {
		h.Store = ctl.Store
	}
	return h
}

// Mount wires the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/tv/changes", h.List)
	r.Post("/api/tv/changes", h.Create)
	r.Get("/api/tv/changes/{id}", h.Get)
	r.Patch("/api/tv/changes/{id}", h.Patch)
	r.Post("/api/tv/changes/{id}/stage", h.Stage)
	r.Post("/api/tv/changes/{id}/verify", h.Verify)
	r.Post("/api/tv/changes/{id}/approve", h.Approve)
	r.Post("/api/tv/changes/{id}/promote", h.Promote)
	r.Post("/api/tv/changes/{id}/cancel", h.Cancel)
	r.Post("/api/tv/changes/{id}/preview", h.Preview)
	r.Post("/api/tv/rollback", h.Rollback)
	r.Get("/api/tv/slots", h.Slots)
	r.Get("/api/tv/releases", h.Releases)
	mountPage(r, h)
}

func admin(w http.ResponseWriter, r *http.Request) (*principal.Principal, bool) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return nil, false
	}
	return p, true
}

func actorOf(p *principal.Principal) *string {
	if p == nil || p.UserID == "" {
		return nil
	}
	u := p.UserID
	return &u
}

func conflict(w http.ResponseWriter, r *http.Request, err error) bool {
	var ce *core.ConflictError
	if errors.As(err, &ce) {
		e := httperror.Conflict("", ce.Error()).With("state", ce.State)
		if ce.InFlight != "" {
			e = e.With("error", "change_in_flight").With("change_id", ce.InFlight)
		}
		httperror.Write(w, r, e)
		return true
	}
	return false
}

func (h *Handler) load(w http.ResponseWriter, r *http.Request) (*core.Change, bool) {
	c, err := h.Store.Get(r.Context(), h.DB, chi.URLParam(r, "id"))
	if errors.Is(err, core.ErrNotFound) {
		httperror.Write(w, r, httperror.NotFound("tv change "+chi.URLParam(r, "id")))
		return nil, false
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load tv change: "+err.Error()))
		return nil, false
	}
	return c, true
}

func (h *Handler) reply(w http.ResponseWriter, r *http.Request, status int, id string) {
	c, err := h.Store.Get(r.Context(), h.DB, id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("reload tv change"))
		return
	}
	c.Events, _ = h.Store.Events(r.Context(), id)
	common.WriteJSON(w, r, status, h.detail(r.Context(), c))
}

func decode(b []byte, into any) {
	if len(b) > 0 {
		_ = json.Unmarshal(b, into)
	}
}

// transition is an audit payload with the states an admin action moved between.
func transition(from, to string, extra map[string]any) map[string]any {
	out := map[string]any{"from": from, "to": to}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// List serves GET /api/tv/changes.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	var states []string
	if s := strings.TrimSpace(r.URL.Query().Get("state")); s != "" {
		states = strings.Split(s, ",")
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.Store.List(r.Context(), states, limit)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list tv changes: "+err.Error()))
		return
	}
	inflight, _ := h.Store.InFlightChange(r.Context(), h.DB)
	cfg, _ := core.LoadConfig(r.Context(), h.DB)
	ids := map[string]bool{}
	for _, c := range items {
		if c.CreatedBy != nil {
			ids[*c.CreatedBy] = true
		}
	}
	out := map[string]any{"items": items, "config": pageConfig(cfg), "users": userNames(r.Context(), h.DB, ids)}
	if inflight != nil {
		out["in_flight"] = inflight.ID
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// Create serves POST /api/tv/changes: a new empty draft an admin fills.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	p, ok := admin(w, r)
	if !ok {
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if e := common.ReadJSON(r, &body, 16<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	c := &core.Change{State: core.StateDraft, Title: strings.TrimSpace(body.Title),
		Source: map[string]any{"kind": core.SourceManual}, Diff: map[string]any{"pending": map[string]any{}},
		CreatedBy: actorOf(p)}
	if err := h.Store.Insert(r.Context(), h.DB, c); err != nil {
		httperror.Write(w, r, httperror.Internal("create tv change: "+err.Error()))
		return
	}
	h.Store.Audit(r.Context(), h.DB, p.UserID, "create", c.ID, transition("", core.StateDraft, nil))
	h.reply(w, r, http.StatusCreated, c.ID)
}

// Get serves GET /api/tv/changes/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	c, ok := h.load(w, r)
	if !ok {
		return
	}
	h.reply(w, r, http.StatusOK, c.ID)
}

// PatchBody is the PATCH body.
type PatchBody struct {
	Title    *string         `json:"title,omitempty"`
	SwapAt   json.RawMessage `json:"swap_at,omitempty"`
	SwapWait *string         `json:"swap_wait,omitempty"`
}

// Patch serves PATCH /api/tv/changes/{id}: title, swap time (null clears it), swap wait.
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	p, ok := admin(w, r)
	if !ok {
		return
	}
	c, ok := h.load(w, r)
	if !ok {
		return
	}
	var body PatchBody
	if e := common.ReadJSON(r, &body, 16<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	fields := map[string]any{}
	if body.Title != nil {
		fields["title"] = strings.TrimSpace(*body.Title)
	}
	if body.SwapWait != nil {
		if *body.SwapWait != "boundary" && *body.SwapWait != "immediate" {
			httperror.Write(w, r, httperror.BadRequest("swap_wait must be boundary or immediate"))
			return
		}
		fields["swap_wait"] = *body.SwapWait
	}
	if len(body.SwapAt) > 0 {
		if _, ok := core.Next(c.State, core.ActionSchedule); !ok {
			httperror.Write(w, r, httperror.Conflict("", "the swap time cannot change once the change is "+c.State).With("state", c.State))
			return
		}
		at, err := parseSwapAt(body.SwapAt)
		if err != nil {
			httperror.Write(w, r, httperror.BadRequest("swap_at must be RFC 3339 or null"))
			return
		}
		fields["swap_at"] = at
	}
	if len(fields) == 0 {
		h.reply(w, r, http.StatusOK, c.ID)
		return
	}
	if err := h.Store.Set(r.Context(), h.DB, c.ID, fields); err != nil {
		httperror.Write(w, r, httperror.Internal("patch tv change: "+err.Error()))
		return
	}
	st := c.State
	_ = h.Store.Event(r.Context(), h.DB, c.ID, core.Event{ActorUserID: actorOf(p), ActorSource: "admin",
		FromState: &st, ToState: &st, Action: core.ActionSchedule, Detail: map[string]any{"fields": keys(fields)}})
	h.Store.Audit(r.Context(), h.DB, p.UserID, "patch", c.ID, transition(st, st, map[string]any{"fields": keys(fields)}))
	h.reply(w, r, http.StatusOK, c.ID)
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func parseSwapAt(raw json.RawMessage) (*time.Time, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, err
	}
	t = t.UTC()
	return &t, nil
}

// Stage serves POST /api/tv/changes/{id}/stage: the tv_stage job builds the release and
// the schedule generation (202: the change moves to staged when it is done).
func (h *Handler) Stage(w http.ResponseWriter, r *http.Request) {
	p, ok := admin(w, r)
	if !ok {
		return
	}
	c, ok := h.load(w, r)
	if !ok {
		return
	}
	if c.State != core.StateDraft {
		httperror.Write(w, r, httperror.Conflict("", "only a draft is staged").With("state", c.State))
		return
	}
	if h.Controller == nil {
		httperror.Write(w, r, httperror.Internal("no change controller"))
		return
	}
	if err := h.Controller.RequestStage(r.Context(), c, actorOf(p)); err != nil {
		if conflict(w, r, err) {
			return
		}
		httperror.Write(w, r, httperror.Internal("stage: "+err.Error()))
		return
	}
	st := c.State
	_ = h.Store.Event(r.Context(), h.DB, c.ID, core.Event{ActorUserID: actorOf(p), ActorSource: "admin",
		FromState: &st, ToState: &st, Action: "stage_requested"})
	h.Store.Audit(r.Context(), h.DB, p.UserID, "stage", c.ID, transition(st, st, nil))
	h.reply(w, r, http.StatusAccepted, c.ID)
}

// Verify serves POST /api/tv/changes/{id}/verify: back to verifying; the controller
// re-runs the checks.
func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	p, ok := admin(w, r)
	if !ok {
		return
	}
	c, ok := h.load(w, r)
	if !ok {
		return
	}
	to, allowed := core.Next(c.State, core.ActionVerify)
	if !allowed {
		httperror.Write(w, r, httperror.Conflict("", "cannot verify a change that is "+c.State).With("state", c.State))
		return
	}
	from := c.State
	if to != c.State {
		if err := h.Store.Move(r.Context(), h.DB, c, to, core.ActionVerify, actorOf(p), nil, nil); err != nil {
			if conflict(w, r, err) {
				return
			}
			httperror.Write(w, r, httperror.Internal("verify: "+err.Error()))
			return
		}
	}
	h.Store.Audit(r.Context(), h.DB, p.UserID, "verify", c.ID, transition(from, c.State, nil))
	h.reply(w, r, http.StatusAccepted, c.ID)
}

// ApproveBody is the approve body.
type ApproveBody struct {
	SwapAt         json.RawMessage `json:"swap_at,omitempty"`
	AckParamChange bool            `json:"ack_param_change,omitempty"`
}

// Approve serves POST /api/tv/changes/{id}/approve (story 33.10 AC5, story 33.12 AC2).
// A ready change becomes approved; a change approved ahead (a draft, staged,
// verifying) carries the approval to ready. A producer's reviewed draft
// (write_channels in review) is approved by posting the queue's Apply with its digest:
// the re-run stages it, already approved.
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	p, ok := admin(w, r)
	if !ok {
		return
	}
	c, ok := h.load(w, r)
	if !ok {
		return
	}
	var body ApproveBody
	if r.ContentLength != 0 {
		if e := common.ReadJSON(r, &body, 16<<10); e != nil {
			httperror.Write(w, r, e)
			return
		}
	}
	to, allowed := core.Next(c.State, core.ActionApprove)
	if !allowed {
		httperror.Write(w, r, httperror.Conflict("", "cannot approve a change that is "+c.State).With("state", c.State))
		return
	}
	cfg, err := core.LoadConfig(r.Context(), h.DB)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tv_staging rules: "+err.Error()))
		return
	}
	if cfg.RequireSecondAdmin {
		// the creator and every admin who saved an edit into the change are its authors
		authors, err := h.Store.Contributors(r.Context(), h.DB, c)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("tv change authors: "+err.Error()))
			return
		}
		for _, u := range authors {
			if u == p.UserID {
				httperror.Write(w, r, httperror.Conflict("", "a second admin approves this change").
					With("state", c.State).With("error", "second_admin_required"))
				return
			}
		}
	}
	if needsParamAck(c) && !body.AckParamChange {
		httperror.Write(w, r, httperror.Conflict("", "this change changes the picture of a pushed channel: acknowledge it").
			With("state", c.State).With("error", "ack_param_change_required"))
		return
	}
	from := c.State
	fields := map[string]any{"approved_by": append(c.ApprovedBy, p.UserID), "approved_at": time.Now().UTC()}
	if len(body.SwapAt) > 0 {
		at, err := parseSwapAt(body.SwapAt)
		if err != nil {
			httperror.Write(w, r, httperror.BadRequest("swap_at must be RFC 3339 or null"))
			return
		}
		fields["swap_at"] = at
	}
	if c.State == core.StateDraft && c.SourceKind() == core.SourceWriteChannels {
		if c.Digest == nil {
			httperror.Write(w, r, httperror.Conflict("", "the draft has no reviewed digest").With("state", c.State))
			return
		}
		if err := h.applyQueue(r.Context(), core.SourceWriteChannels, *c.Digest, c.ID); err != nil {
			httperror.Write(w, r, httperror.Internal("apply: "+err.Error()))
			return
		}
	}
	if to == c.State {
		if err := h.Store.Set(r.Context(), h.DB, c.ID, fields); err != nil {
			httperror.Write(w, r, httperror.Internal("approve: "+err.Error()))
			return
		}
		st := c.State
		_ = h.Store.Event(r.Context(), h.DB, c.ID, core.Event{ActorUserID: actorOf(p), ActorSource: "admin",
			FromState: &st, ToState: &st, Action: "approve_ahead"})
		if c.State == core.StateDraft && c.SourceKind() != core.SourceWriteChannels && h.Controller != nil {
			c.ApprovedBy = append(c.ApprovedBy, p.UserID)
			if err := h.Controller.RequestStage(r.Context(), c, actorOf(p)); err != nil && !isConflict(err) {
				httperror.Write(w, r, httperror.Internal("stage: "+err.Error()))
				return
			}
		}
	} else if err := h.Store.Move(r.Context(), h.DB, c, to, core.ActionApprove, actorOf(p), nil, fields); err != nil {
		if conflict(w, r, err) {
			return
		}
		httperror.Write(w, r, httperror.Internal("approve: "+err.Error()))
		return
	}
	h.Store.Audit(r.Context(), h.DB, p.UserID, "approve", c.ID,
		transition(from, c.State, map[string]any{"ack_param_change": body.AckParamChange}))
	h.reply(w, r, http.StatusOK, c.ID)
}

func isConflict(err error) bool {
	var ce *core.ConflictError
	return errors.As(err, &ce)
}

// needsParamAck reports whether verification asks for the parameter-change
// acknowledgement (push.continuity, story 33.5/33.8; never set until then).
func needsParamAck(c *core.Change) bool {
	checks, _ := c.VerifyReport["checks"].([]any)
	for _, x := range checks {
		m, _ := x.(map[string]any)
		if m["check"] == "push.continuity" && m["result"] == core.ResultFail {
			return true
		}
	}
	return false
}

func (h *Handler) applyQueue(ctx context.Context, stage, digest, changeID string) error {
	if h.ApplyQueue != nil {
		return h.ApplyQueue(ctx, stage, digest, changeID)
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	payload := map[string]any{"apply": true, "plan_digests": []string{digest}, "tv_change_id": changeID}
	res, err := queues.Enqueue(ctx, tx, queues.EnqueueParams{SubjectKind: "catalog", SubjectID: core.CatalogID,
		Stage: stage, Force: true, Payload: payload,
		Reason: queues.ReasonFor(ctx, "apply", map[string]any{"tv_change_id": changeID})})
	if err != nil {
		return err
	}
	if res.JobID != nil {
		d, _ := json.Marshal([]string{digest})
		if _, err := tx.ExecContext(ctx, `UPDATE processing_jobs
			SET payload = COALESCE(payload, '{}'::jsonb)
			    || jsonb_build_object('apply', true, 'plan_digests', (
			         SELECT COALESCE(jsonb_agg(DISTINCT d), '[]'::jsonb)
			           FROM jsonb_array_elements(COALESCE(payload -> 'plan_digests', '[]'::jsonb) || $2::jsonb) AS d))
			WHERE id = $1`, *res.JobID, string(d)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Promote serves POST /api/tv/changes/{id}/promote (Swap now).
func (h *Handler) Promote(w http.ResponseWriter, r *http.Request) {
	p, ok := admin(w, r)
	if !ok {
		return
	}
	c, ok := h.load(w, r)
	if !ok {
		return
	}
	if h.Controller == nil {
		httperror.Write(w, r, httperror.Internal("no change controller"))
		return
	}
	from := c.State
	if err := h.Controller.Promote(r.Context(), c, actorOf(p)); err != nil {
		if conflict(w, r, err) {
			return
		}
		httperror.Write(w, r, httperror.Internal("promote: "+err.Error()))
		return
	}
	h.Store.Audit(r.Context(), h.DB, p.UserID, "promote", c.ID, transition(from, c.State, nil))
	h.reply(w, r, http.StatusAccepted, c.ID)
}

// Cancel serves POST /api/tv/changes/{id}/cancel.
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	p, ok := admin(w, r)
	if !ok {
		return
	}
	c, ok := h.load(w, r)
	if !ok {
		return
	}
	to, allowed := core.Next(c.State, core.ActionCancel)
	if !allowed {
		httperror.Write(w, r, httperror.Conflict("", "cannot cancel a change that is "+c.State).With("state", c.State))
		return
	}
	from := c.State
	if err := h.Store.Move(r.Context(), h.DB, c, to, core.ActionCancel, actorOf(p), nil,
		map[string]any{"ended_at": time.Now().UTC(), "end_reason": "cancelled"}); err != nil {
		if conflict(w, r, err) {
			return
		}
		httperror.Write(w, r, httperror.Internal("cancel: "+err.Error()))
		return
	}
	h.Store.Audit(r.Context(), h.DB, p.UserID, "cancel", c.ID, transition(from, c.State, nil))
	h.reply(w, r, http.StatusOK, c.ID)
}

func notYet(w http.ResponseWriter, r *http.Request, what, story string) {
	httperror.Write(w, r, &httperror.Error{Type: httperror.TypeConflict, Title: "not available yet",
		Status: http.StatusNotImplemented, Detail: what + " arrives with story " + story})
}

// previewStates are the states a staged change can be previewed in (33.6 AC1).
var previewStates = map[string]bool{core.StateVerifying: true, core.StateReady: true, core.StateApproved: true}

// PreviewChannel is one channel of a preview.
type PreviewChannel struct {
	ID      string `json:"id"`
	Number  int    `json:"number"`
	Name    string `json:"name"`
	Logo    bool   `json:"logo"`
	Changed bool   `json:"changed,omitempty"`
	Added   bool   `json:"added,omitempty"`
	Removed bool   `json:"removed,omitempty"`
	// URL is the staging playlist (web origin: the stream gate wants an admin
	// session; the token rides along); LiveURL the live one, side by side.
	URL     string `json:"url"`
	LiveURL string `json:"live_url,omitempty"`
}

// PreviewReply is POST /api/tv/changes/{id}/preview.
type PreviewReply struct {
	HLSBase   string           `json:"hls_base"`
	Token     string           `json:"token"`
	M3U       string           `json:"m3u"`
	XMLTV     string           `json:"xmltv"`
	ExpiresAt time.Time        `json:"expires_at"`
	Channels  []PreviewChannel `json:"channels"`
}

// Preview serves POST /api/tv/changes/{id}/preview (story 33.6): signed staging
// URLs for an hour — the staging slot's HLS per channel (through the web port's
// stream gate, admin sessions only), the staged lineup M3U and the staged guide
// (on the streaming port, for VLC). Never a second HDHomeRun device.
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	p, ok := admin(w, r)
	if !ok {
		return
	}
	c, ok := h.load(w, r)
	if !ok {
		return
	}
	if !previewStates[c.State] || c.ReleaseID == nil {
		httperror.Write(w, r, httperror.Conflict("", "a change can be previewed while verifying, ready or approved; this one is "+c.State).
			With("state", c.State))
		return
	}
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	tok, err := core.PreviewToken(r.Context(), h.DB, c.ID, exp)
	if err != nil {
		httperror.Write(w, r, httperror.Conflict("", err.Error()).With("state", c.State))
		return
	}
	q := "?pt=" + url.QueryEscape(tok)
	base := strings.TrimRight(os.Getenv("MAKTABA_PUBLIC_BASE_URL"), "/")
	out := PreviewReply{HLSBase: "/stream/staging/channel/", Token: tok, M3U: base + "/staging/lineup.m3u" + q,
		XMLTV: base + "/staging/xmltv.xml" + q, ExpiresAt: exp, Channels: []PreviewChannel{}}
	changed, added, removed := map[string]bool{}, map[string]bool{}, map[string]bool{}
	if lu, ok := c.Diff["lineup"].(map[string]any); ok {
		for k, set := range map[string]map[string]bool{"added": added, "removed": removed, "renumbered": changed,
			"renamed": changed, "changed": changed} {
			xs, _ := lu[k].([]any)
			for _, x := range xs {
				if m, ok := x.(map[string]any); ok {
					if id, _ := m["id"].(string); id != "" {
						set[id] = true
					}
				}
			}
		}
	}
	for _, k := range []string{"schedules", "art"} {
		xs, _ := c.Diff[k].([]any)
		for _, x := range xs {
			if m, ok := x.(map[string]any); ok {
				if id, _ := m["id"].(string); id != "" {
					changed[id] = true
				}
			}
		}
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT channel_id, def FROM tv_release_channels WHERE release_id = $1`, *c.ReleaseID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("preview: "+err.Error()))
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		if rows.Scan(&id, &raw) != nil {
			continue
		}
		var d struct {
			Number   int     `json:"number"`
			Name     string  `json:"name"`
			LogoPath *string `json:"logo_path"`
			Enabled  *bool   `json:"enabled"`
		}
		if json.Unmarshal(raw, &d) != nil || (d.Enabled != nil && !*d.Enabled) {
			continue
		}
		pc := PreviewChannel{ID: id, Number: d.Number, Name: d.Name, Logo: d.LogoPath != nil && *d.LogoPath != "",
			Changed: changed[id], Added: added[id], URL: out.HLSBase + id + "/index.m3u8" + q}
		if !added[id] {
			pc.LiveURL = "/stream/channel/" + id + "/index.m3u8"
		}
		out.Channels = append(out.Channels, pc)
	}
	// EC1: a removed channel is shown from live, labelled
	if lu, ok := c.Diff["lineup"].(map[string]any); ok {
		xs, _ := lu["removed"].([]any)
		for _, x := range xs {
			m, _ := x.(map[string]any)
			id, _ := m["id"].(string)
			if id == "" {
				continue
			}
			n, _ := m["number"].(float64)
			name, _ := m["name"].(string)
			out.Channels = append(out.Channels, PreviewChannel{ID: id, Number: int(n), Name: name, Removed: true,
				URL: out.HLSBase + id + "/index.m3u8" + q, LiveURL: "/stream/channel/" + id + "/index.m3u8"})
		}
	}
	sort.SliceStable(out.Channels, func(i, j int) bool { return out.Channels[i].Number < out.Channels[j].Number })
	h.Store.Audit(r.Context(), h.DB, p.UserID, "preview", c.ID, map[string]any{"expires_at": exp})
	common.WriteJSON(w, r, http.StatusOK, out)
}

// Rollback serves POST /api/tv/rollback (story 33.9).
func (h *Handler) Rollback(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	notYet(w, r, "rollback", "33.9")
}

// Slot is one TV slot of a streaming host (tv_slots, story 33.1).
type Slot struct {
	HostID       string     `json:"host_id"`
	Slot         string     `json:"slot"`
	Role         string     `json:"role"`
	State        string     `json:"state"`
	ReleaseID    *string    `json:"release_id,omitempty"`
	Version      string     `json:"version,omitempty"`
	Epoch        int64      `json:"epoch"`
	PID          int        `json:"pid,omitempty"`
	Warm         []string   `json:"warm"`
	EncodersUsed int        `json:"encoders_used"`
	BeatAt       *time.Time `json:"beat_at,omitempty"`
}

// slotUnhealthyAfter: three missed beats (story 33.1 AC4).
const slotUnhealthyAfter = 15 * time.Second

// Slots serves GET /api/tv/slots: every host's TV slots from tv_slots — role
// (live, standby, staging, ...) and routing epoch, state (a slot missing three
// beats is unhealthy), release, version, warm channels, encoders. A home that runs
// the engine in one process (no slot rows: [tv] slots = off, or before 33.1) is
// reported as one live slot.
func (h *Handler) Slots(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	out := []Slot{}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT host_id, slot, role, state, release_id, COALESCE(version, ''),
		role_epoch, COALESCE(pid, 0), COALESCE(warm, '[]'), encoders_used, beat_at
		FROM tv_slots ORDER BY host_id, slot`)
	if err == nil {
		defer rows.Close()
		now := time.Now()
		for rows.Next() {
			var s Slot
			var rel sql.NullString
			var warm []byte
			var beat timeScan
			if rows.Scan(&s.HostID, &s.Slot, &s.Role, &s.State, &rel, &s.Version, &s.Epoch, &s.PID, &warm,
				&s.EncodersUsed, &beat) != nil {
				continue
			}
			if rel.Valid {
				v := rel.String
				s.ReleaseID = &v
			}
			s.Warm = []string{}
			_ = json.Unmarshal(warm, &s.Warm)
			if t := beat.t; !t.IsZero() {
				s.BeatAt = &t
				if s.State == "healthy" && now.Sub(t) > slotUnhealthyAfter {
					s.State = "unhealthy"
				}
			} else if s.State == "healthy" {
				s.State = "starting"
			}
			out = append(out, s)
		}
	}
	runtime := "slots"
	if err != nil || len(out) == 0 {
		runtime = "single"
		out = out[:0]
		var rel sql.NullString
		_ = h.DB.QueryRowContext(r.Context(), `SELECT live_release_id FROM tv_routing WHERE scope = 'cluster'`).Scan(&rel)
		s := Slot{HostID: "home", Slot: "blue", Role: "live", State: "healthy", Warm: []string{}}
		if rel.Valid {
			v := rel.String
			s.ReleaseID = &v
		}
		out = append(out, s)
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"runtime": runtime, "slots": out})
}

// Release is a tv_releases row over the wire.
type Release struct {
	ID               string    `json:"id"`
	Digest           string    `json:"digest"`
	Kinds            []string  `json:"kinds"`
	SchedGen         int64     `json:"sched_gen"`
	StreamingVersion string    `json:"streaming_version"`
	BaseReleaseID    *string   `json:"base_release_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	Live             bool      `json:"live"`
	Changes          []string  `json:"changes"`
}

// Releases serves GET /api/tv/releases (story 33.2 AC6).
func (h *Handler) Releases(w http.ResponseWriter, r *http.Request) {
	if _, ok := admin(w, r); !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var live sql.NullString
	_ = h.DB.QueryRowContext(r.Context(), `SELECT live_release_id FROM tv_routing WHERE scope = 'cluster'`).Scan(&live)
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id, digest, kinds, sched_gen, streaming_version,
		base_release_id, created_at FROM tv_releases ORDER BY sched_gen DESC LIMIT $1`, limit)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list releases: "+err.Error()))
		return
	}
	defer rows.Close()
	out := []Release{}
	for rows.Next() {
		var rl Release
		var kinds []byte
		var base sql.NullString
		var at timeScan
		if err := rows.Scan(&rl.ID, &rl.Digest, &kinds, &rl.SchedGen, &rl.StreamingVersion, &base, &at); err != nil {
			httperror.Write(w, r, httperror.Internal("list releases: "+err.Error()))
			return
		}
		rl.Kinds = []string{}
		_ = json.Unmarshal(kinds, &rl.Kinds)
		if base.Valid {
			v := base.String
			rl.BaseReleaseID = &v
		}
		rl.CreatedAt = at.t
		rl.Live = live.Valid && live.String == rl.ID
		out = append(out, rl)
	}
	if err := rows.Err(); err != nil {
		httperror.Write(w, r, httperror.Internal("list releases: "+err.Error()))
		return
	}
	for i := range out {
		out[i].Changes = []string{}
		cr, err := h.DB.QueryContext(r.Context(), `SELECT id FROM tv_changes WHERE release_id = $1 ORDER BY created_at`, out[i].ID)
		if err != nil {
			continue
		}
		for cr.Next() {
			var id string
			if cr.Scan(&id) == nil {
				out[i].Changes = append(out[i].Changes, id)
			}
		}
		cr.Close()
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": out})
}

// timeScan reads a native or text timestamp.
type timeScan struct{ t time.Time }

func (s *timeScan) Scan(v any) error {
	switch x := v.(type) {
	case time.Time:
		s.t = x.UTC()
	case string:
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			s.t = t.UTC()
		} else if t, err := time.Parse("2006-01-02 15:04:05", x); err == nil {
			s.t = t.UTC()
		}
	case []byte:
		return s.Scan(string(x))
	case nil:
		s.t = time.Time{}
	}
	return nil
}

// StagingInfo is /api/tv/info's staging part (README "Extended").
type StagingInfo struct {
	Enabled        bool    `json:"enabled"`
	LiveReleaseID  *string `json:"live_release"`
	LiveGen        int64   `json:"live_gen"`
	ChangeInFlight *string `json:"change_in_flight"`
	// InFlightNumber / InFlightState: the "#42 verifying" of the Live TV banner.
	InFlightNumber int64  `json:"change_in_flight_number,omitempty"`
	InFlightState  string `json:"change_in_flight_state,omitempty"`
}

// Info is what /api/tv/info reports about staging.
func Info(ctx context.Context, db *sql.DB) any {
	cfg, _ := core.LoadConfig(ctx, db)
	out := StagingInfo{Enabled: cfg.Enabled}
	var rel sql.NullString
	var gen sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT live_release_id, live_gen FROM tv_routing WHERE scope = 'cluster'`).Scan(&rel, &gen); err == nil {
		if rel.Valid {
			v := rel.String
			out.LiveReleaseID = &v
		}
		out.LiveGen = gen.Int64
	}
	if c, err := (&core.Store{DB: db}).InFlightChange(ctx, db); err == nil && c != nil {
		id := c.ID
		out.ChangeInFlight = &id
		out.InFlightNumber, out.InFlightState = c.Number, c.State
	}
	return out
}
