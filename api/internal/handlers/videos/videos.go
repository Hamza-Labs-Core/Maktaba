// Package videos implements Story 7.4 (list/detail/patch/delete),
// Story 7.5 (process/reprocess), Story 7.6 (transcript window), and
// the read-only enumerations from Story 7.7 (subtitles/chapters lists
// scoped to a video).
//
// Routes:
//
//	GET    /api/videos
//	GET    /api/videos/{id}
//	PATCH  /api/videos/{id}
//	DELETE /api/videos/{id}
//	POST   /api/videos/{id}/process
//	POST   /api/videos/{id}/reprocess
//	GET    /api/videos/{id}/segments
//	GET    /api/videos/{id}/subtitles
//	GET    /api/videos/{id}/chapters
//
// State is owned by the Pipeline; this package only validates and
// proxies. Tag delta semantics are owned by Story 7.14 (collections/tags).
package videos

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/authz"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/sharepath"
)

// Video is the over-the-wire shape for both list and detail responses.
// Detail joins audio_tracks / chapters / tags / latest transcript; the
// list endpoint sets the join fields to nil.
type Video struct {
	ID            string          `json:"id"`
	LibraryID     string          `json:"library_id"`
	Path          string          `json:"path"`
	Filename      string          `json:"filename"`
	Title         *string         `json:"title,omitempty"`
	Description   *string         `json:"description,omitempty"`
	State         string          `json:"state"`
	DetectedLang  *string         `json:"detected_language,omitempty"`
	DurationSec   *float64        `json:"duration_sec,omitempty"`
	SizeBytes     int64           `json:"size_bytes"`
	ContentHash   string          `json:"content_hash"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	Playable      *bool           `json:"playable,omitempty"`
	Tags          []string        `json:"tags,omitempty"`
	LatestTrans   *string         `json:"latest_transcript_id,omitempty"`
	PlaybackState *PlaybackState  `json:"playback_state,omitempty"`

	// Display titles (Story 27.11, slot 0122): the short on-air title the
	// guide and the channel strip show. DisplayTitle is the effective one
	// (the hand-set one, else the automatic one); DisplayTitleSource is
	// "manual", "rules" or "llm" ("" before the stage ran).
	DisplayTitle       *string `json:"display_title,omitempty"`
	DisplayTitleAuto   *string `json:"display_title_auto,omitempty"`
	DisplayTitleManual *string `json:"display_title_manual,omitempty"`
	DisplayTitleSource string  `json:"display_title_source,omitempty"`
}

// PlaybackState is the embedded shape on detail.
type PlaybackState struct {
	PositionSec float64   `json:"position_sec"`
	Completed   bool      `json:"completed"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PatchRequest is the AC-3 schema. Other fields in the body are
// silently ignored (json.Unmarshal into this struct drops them).
type PatchRequest struct {
	Title       *string  `json:"title,omitempty"`
	Description *string  `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// Handler bundles the deps. Authz is optional in single-user / admin-
// token environments; when nil every request is treated as authorized
// past principal.
type Handler struct {
	DB      *sql.DB
	Authz   authz.Authz
	JobQ    JobControl
	Audit   AuditWriter
	NowFunc func() time.Time
	// DisplayTitleRefresh, when set, is called after a display title was
	// set or cleared by hand: it enqueues the library's display_title stage
	// so the channels' future blocks get the new title. Best effort.
	DisplayTitleRefresh func(ctx context.Context, libraryID string) error
}

// DisplayTitleMaxRunes caps a hand-set display title.
const DisplayTitleMaxRunes = 200

// JobControl is the Story 7.5 surface: enqueue a per-video job
// (or bump an existing one).
type JobControl interface {
	BumpOrEnqueue(ctx context.Context, videoID, stage string, priority int) (jobID string, err error)
	RollbackToStage(ctx context.Context, videoID, fromStage string, priority int) (jobIDs []string, err error)
}

// AuditWriter records the video mutations to audit_log.
type AuditWriter interface {
	Write(ctx context.Context, category, action, actorUser, targetID string, payload map[string]any) error
}

// Video audit actions (category 'library', target = the video id, the
// video's library_id in the payload).
const (
	AuditVideoPatched     = "video-patched"
	AuditVideoDeleted     = "video-deleted"
	AuditVideoPurged      = "video-purge"
	AuditVideoProcessed   = "video-process"
	AuditVideoReprocessed = "video-reprocess"
)

// DBAudit is the production AuditWriter: one best-effort audit_log row.
type DBAudit struct{ DB *sql.DB }

// Write inserts the row. A non-UUID actor is stored as NULL (the column
// references users).
func (a DBAudit) Write(ctx context.Context, category, action, actorUser, targetID string, payload map[string]any) error {
	if a.DB == nil {
		return nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var actor any
	if _, perr := uuid.Parse(actorUser); perr == nil {
		actor = actorUser
	}
	_, err = a.DB.ExecContext(ctx, `
		INSERT INTO audit_log (category, action, actor_user_id, target_id, payload)
		VALUES ($1, $2, $3, $4, $5)
	`, category, action, actor, targetID, string(b))
	return err
}

// audit writes one video audit row; a failed write never fails the
// request (it is logged).
func (h *Handler) audit(r *http.Request, action string, v Video, payload map[string]any) {
	if h.Audit == nil {
		return
	}
	actor := ""
	if p := principal.FromContext(r.Context()); p != nil {
		actor = p.UserID
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["library_id"] = v.LibraryID
	if err := h.Audit.Write(r.Context(), "library", action, actor, v.ID, payload); err != nil {
		slog.Warn("videos: audit row not written", "video", v.ID, "action", action, "err", err.Error())
	}
}

// Mount attaches the routes on r.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/videos", h.List)
	r.Get("/api/videos/{id}", h.Detail)
	r.Get("/api/videos/{id}/poster", h.Poster)
	r.Patch("/api/videos/{id}", h.Patch)
	r.Delete("/api/videos/{id}", h.Delete)
	r.Post("/api/videos/{id}/process", h.Process)
	r.Post("/api/videos/{id}/reprocess", h.Reprocess)
	r.Get("/api/videos/{id}/segments", h.Segments)
	r.Get("/api/videos/{id}/subtitles", h.Subtitles)
	r.Get("/api/videos/{id}/chapters", h.Chapters)
}

// List implements AC-1. Filters: library, language, q, sort, limit and
// cursor (`next` of the previous page).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "authentication required"))
		return
	}

	q := r.URL.Query()
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
	sort := q.Get("sort")
	if sort == "" {
		sort = "updated_at"
	}
	if sort != "updated_at" && sort != "created_at" && sort != "title" {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/invalid-sort",
			Title:  "invalid sort",
			Status: http.StatusBadRequest,
			Detail: "sort must be one of: updated_at, created_at, title",
		})
		return
	}

	var args []any
	conds := []string{}
	if libID := q.Get("library"); libID != "" {
		// A malformed id used to be ignored, which listed every video.
		if _, err := uuid.Parse(libID); err != nil {
			httperror.Write(w, r, httperror.BadRequest("library must be a UUID"))
			return
		}
		conds = append(conds, placeholder("library_id = ", len(args)+1))
		args = append(args, libID)
	}
	if lang := q.Get("language"); lang != "" {
		conds = append(conds, placeholder("detected_language = ", len(args)+1))
		args = append(args, lang)
	}
	if qstr := strings.TrimSpace(q.Get("q")); qstr != "" {
		// Cheap LIKE search — Story 7.8 owns the real FTS path.
		conds = append(conds, placeholder("(title LIKE ", len(args)+1)+" OR description LIKE "+pidx(len(args)+1)+")")
		args = append(args, "%"+qstr+"%")
	}
	// Authz scope: non-admin users only see videos in libraries they own.
	if !p.AccessAllLibraries && len(p.Libraries) > 0 {
		placeholders := make([]string, len(p.Libraries))
		for i, lid := range p.Libraries {
			placeholders[i] = pidx(len(args) + 1)
			args = append(args, lid)
		}
		conds = append(conds, "library_id IN ("+strings.Join(placeholders, ",")+")")
	} else if !p.AccessAllLibraries {
		// User has no libraries — return empty.
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": []Video{}, "next": nil})
		return
	}

	cur, cerr := decodeListCursor(q.Get("cursor"), sort)
	if cerr != nil {
		httperror.Write(w, r, cerr)
		return
	}
	if cur != nil {
		frag, cargs, werr := cur.where(sort, len(args)+1)
		if werr != nil {
			httperror.Write(w, r, werr)
			return
		}
		conds = append(conds, frag)
		args = append(args, cargs...)
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	args = append(args, limit+1) // SELECT limit+1 to detect another page.
	query := "SELECT " + videoColumns + " FROM videos" + where +
		" ORDER BY " + sortExpr(sort) + " DESC, id DESC LIMIT " + pidx(len(args))

	rows, err := h.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("query videos: "+err.Error()))
		return
	}
	defer rows.Close()
	items := []Video{}
	for rows.Next() {
		v, err := scanVideoRow(rows)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("scan video: "+err.Error()))
			return
		}
		items = append(items, v)
	}

	var next *string
	if len(items) > limit {
		items = items[:limit]
		// Pass it back as ?cursor= (with the same sort) for the next page.
		s := encodeListCursor(sort, items[len(items)-1])
		next = &s
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items, "next": next})
}

// Detail implements AC-2.
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	v, err := h.loadVideo(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("video "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load video").Wrap(err))
		return
	}
	if err := h.canRead(r.Context(), v.LibraryID); err != nil {
		httperror.Write(w, r, err)
		return
	}

	// File presence check (EC: drive unmounted). Cheap stat; if it
	// returns ENOENT, mark not playable.
	playable := true
	if v.Path != "" {
		// story 31.3: a canonical smb://... path is the home's mount of the share
		if _, err := os.Stat(sharepath.ToLocal(v.Path)); err != nil {
			playable = false
		}
	}
	v.Playable = &playable
	h.loadDisplay(r.Context(), &v)

	// Tags, the latest active transcript and this user's playback
	// state come from the batch readers the GraphQL layer shares
	// (batch.go). Each is best-effort, as before.
	ids := []string{id}
	if tags, err := TagsByVideo(r.Context(), h.DB, ids); err == nil {
		for _, t := range tags[id] {
			v.Tags = append(v.Tags, t.Name)
		}
	}
	if trans, err := ActiveTranscripts(r.Context(), h.DB, ids); err == nil {
		if t, ok := trans[id]; ok {
			transID := t.ID
			v.LatestTrans = &transID
		}
	}
	if p := principal.FromContext(r.Context()); p != nil && p.UserID != "" {
		if states, err := PlaybackStates(r.Context(), h.DB, p.UserID, ids); err == nil {
			if ps, ok := states[id]; ok {
				v.PlaybackState = &ps
			}
		}
	}

	common.WriteJSON(w, r, http.StatusOK, v)
}

// Patch implements AC-3: title/description/tags only. Body cap is
// 8 KiB (story 7.19 EC).
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}

	// Allow extra fields in the patch body — we silently ignore them
	// per AC-3 — so we decode into a permissive map first then filter.
	var raw map[string]json.RawMessage
	if e := common.ReadJSON(r, &raw, 8<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}

	v, err := h.loadVideo(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("video "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load video").Wrap(err))
		return
	}
	if err := h.canWrite(r.Context(), v.LibraryID); err != nil {
		httperror.Write(w, r, err)
		return
	}

	// Update only known fields. `edited` tracks the columns the user
	// changed so we can stamp `media_field_provenance(origin='user')`
	// after the write (Epic 26 Story 26.6: a user edit makes a field
	// user-owned, so a later enrichment accept skips it).
	sets := []string{}
	args := []any{}
	edited := map[string]string{}
	if raw["title"] != nil {
		var s string
		if err := json.Unmarshal(raw["title"], &s); err == nil {
			sets = append(sets, "title = "+pidx(len(args)+1))
			args = append(args, s)
			edited["title"] = s
		}
	}
	if raw["description"] != nil {
		var s string
		if err := json.Unmarshal(raw["description"], &s); err == nil {
			sets = append(sets, "description = "+pidx(len(args)+1))
			args = append(args, s)
			edited["description"] = s
		}
	}
	displayChanged := false
	if dt, present := raw["display_title"]; present {
		manual, ferr := parseDisplayTitle(dt)
		if ferr != nil {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{*ferr}))
			return
		}
		sets = append(sets, "display_title_manual = "+pidx(len(args)+1))
		args = append(args, manual)
		displayChanged = true
	}
	if len(sets) > 0 {
		now := h.now()
		sets = append(sets, "updated_at = "+pidx(len(args)+1))
		args = append(args, now)
		args = append(args, id)
		q := "UPDATE videos SET " + strings.Join(sets, ", ") + " WHERE id = " + pidx(len(args))
		if _, err := h.DB.ExecContext(r.Context(), q, args...); err != nil {
			httperror.Write(w, r, httperror.Internal("update video").Wrap(err))
			return
		}
		h.recordUserProvenance(r.Context(), id, edited, now)
	}
	if displayChanged && h.DisplayTitleRefresh != nil {
		if err := h.DisplayTitleRefresh(r.Context(), v.LibraryID); err != nil {
			slog.Warn("videos: display title refresh not queued", "video", id, "err", err.Error(),
				"event", "display_title_refresh_failed")
		}
	}

	// Tags replace — AC-3 "a flat tags array on PATCH replaces the set".
	if raw["tags"] != nil {
		var tags []string
		if err := json.Unmarshal(raw["tags"], &tags); err == nil {
			if err := h.replaceTags(r.Context(), id, tags); err != nil {
				httperror.Write(w, r, httperror.Internal("update tags").Wrap(err))
				return
			}
		}
	}

	fields := []string{}
	for _, k := range []string{"title", "description", "display_title", "tags"} {
		if _, ok := raw[k]; ok {
			fields = append(fields, k)
		}
	}
	h.audit(r, AuditVideoPatched, v, map[string]any{"fields": fields})

	v2, _ := h.loadVideo(r.Context(), id)
	h.loadDisplay(r.Context(), &v2)
	common.WriteJSON(w, r, http.StatusOK, v2)
}

// Delete implements AC-4. ?purge=true requires ?confirm=<video_id>.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	v, err := h.loadVideo(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("video "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load video").Wrap(err))
		return
	}
	if err := h.canWrite(r.Context(), v.LibraryID); err != nil {
		httperror.Write(w, r, err)
		return
	}

	purge, e := common.QueryBool(r, "purge", false)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if purge {
		if r.URL.Query().Get("confirm") != id {
			httperror.Write(w, r, &httperror.Error{
				Type:   httperror.TypeConfirmationReq,
				Title:  "confirmation required",
				Status: http.StatusPreconditionFailed,
				Detail: "include ?confirm=<video_id> to purge",
			})
			return
		}
	}

	if _, err := h.DB.ExecContext(r.Context(), `DELETE FROM videos WHERE id=$1`, id); err != nil {
		httperror.Write(w, r, httperror.Internal("delete video").Wrap(err))
		return
	}
	warn := ""
	if purge {
		if err := os.Remove(v.Path); err != nil {
			if os.IsNotExist(err) {
				warn = "file-not-found"
			}
		}
		h.audit(r, AuditVideoPurged, v, map[string]any{"path": v.Path, "file_missing": warn != ""})
	} else {
		h.audit(r, AuditVideoDeleted, v, map[string]any{"path": v.Path})
	}
	if warn != "" {
		w.Header().Set("Maktaba-Warning", warn)
	}
	common.WriteNoContent(w)
}

// Process implements 7.5 AC-1.
type ProcessRequest struct {
	Stage    string `json:"stage,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

func (h *Handler) Process(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	v, err := h.loadVideo(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("video "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load video").Wrap(err))
		return
	}
	if err := h.canWrite(r.Context(), v.LibraryID); err != nil {
		httperror.Write(w, r, err)
		return
	}
	var req ProcessRequest
	_ = common.ReadJSON(r, &req, 4<<10) // body optional
	stage := req.Stage
	if stage == "" {
		stage = "transcribe"
	}
	prio := req.Priority
	if prio == 0 {
		prio = 50
	}
	if h.JobQ == nil {
		h.audit(r, AuditVideoProcessed, v, map[string]any{"stage": stage, "priority": prio, "job_id": ""})
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"job_id": "", "note": "no-op (no enqueuer)"})
		return
	}
	jobID, err := h.JobQ.BumpOrEnqueue(r.Context(), id, stage, prio)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("enqueue: "+err.Error()))
		return
	}
	h.audit(r, AuditVideoProcessed, v, map[string]any{"stage": stage, "priority": prio, "job_id": jobID})
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"job_id": jobID})
}

// Reprocess implements 7.5 AC-2.
type ReprocessRequest struct {
	FromStage string `json:"from_stage"`
}

func (h *Handler) Reprocess(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	v, err := h.loadVideo(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("video "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load video").Wrap(err))
		return
	}
	if err := h.canWrite(r.Context(), v.LibraryID); err != nil {
		httperror.Write(w, r, err)
		return
	}
	var req ReprocessRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.FromStage == "scan" {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/stage-not-per-video",
			Title:  "stage not per-video",
			Status: http.StatusBadRequest,
			Detail: "scan is library-scoped",
		})
		return
	}
	if h.JobQ == nil {
		h.audit(r, AuditVideoReprocessed, v, map[string]any{"from_stage": req.FromStage, "job_ids": []string{}})
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"job_ids": []string{}})
		return
	}
	ids, err := h.JobQ.RollbackToStage(r.Context(), id, req.FromStage, 200)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("reprocess: "+err.Error()))
		return
	}
	h.audit(r, AuditVideoReprocessed, v, map[string]any{"from_stage": req.FromStage, "job_ids": ids})
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"job_ids": ids})
}

// parseDisplayTitle reads the PATCH display_title value: a string sets the
// hand-set title (trimmed); "" or null clears it (nil).
func parseDisplayTitle(raw json.RawMessage) (any, *httperror.FieldError) {
	if string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, &httperror.FieldError{Field: "display_title", Message: "must be a string or null"}
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(s) > DisplayTitleMaxRunes {
		return nil, &httperror.FieldError{Field: "display_title", Message: "at most 200 characters"}
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return nil, &httperror.FieldError{Field: "display_title", Message: "must not contain control characters"}
		}
	}
	return s, nil
}

// loadDisplay fills the display-title fields (slot 0122). Best effort: a
// database without the columns leaves them empty.
func (h *Handler) loadDisplay(ctx context.Context, v *Video) {
	var auto, manual, src sql.NullString
	err := h.DB.QueryRowContext(ctx, `
		SELECT display_title, display_title_manual, display_title_source FROM videos WHERE id = $1
	`, v.ID).Scan(&auto, &manual, &src)
	if err != nil {
		return
	}
	fillDisplay(v, auto, manual, src)
}

func fillDisplay(v *Video, auto, manual, src sql.NullString) {
	if auto.Valid {
		a := auto.String
		v.DisplayTitleAuto = &a
		v.DisplayTitle = &a
		v.DisplayTitleSource = src.String
	}
	if manual.Valid && strings.TrimSpace(manual.String) != "" {
		m := manual.String
		v.DisplayTitleManual = &m
		v.DisplayTitle = &m
		v.DisplayTitleSource = "manual"
	}
}

// loadVideo reads the row by id; sql.ErrNoRows surfaces unchanged.
func (h *Handler) loadVideo(ctx context.Context, id string) (Video, error) {
	row := h.DB.QueryRowContext(ctx, `SELECT `+videoColumns+` FROM videos WHERE id = $1`, id)
	return scanVideoRow(row)
}

// recordUserProvenance stamps each user-edited field as user-owned in
// `media_field_provenance` (Epic 26 Story 26.6). Best-effort: a failure
// here (e.g. the slot-0078 table not yet migrated) must never fail the
// PATCH, so errors are swallowed.
func (h *Handler) recordUserProvenance(ctx context.Context, videoID string, edited map[string]string, now time.Time) {
	for field := range edited {
		_, _ = h.DB.ExecContext(ctx, `
			INSERT INTO media_field_provenance (video_id, field, origin, set_at)
			VALUES ($1, $2, 'user', $3)
			ON CONFLICT (video_id, field) DO UPDATE
			  SET origin = 'user', set_at = EXCLUDED.set_at
		`, videoID, field, now)
	}
}

// canRead returns nil if the principal may read the library_id.
func (h *Handler) canRead(ctx context.Context, libraryID string) *httperror.Error {
	p := principal.FromContext(ctx)
	if p == nil {
		return httperror.Forbidden("", "authentication required")
	}
	if CanRead(p, libraryID) {
		return nil
	}
	return httperror.Forbidden("", "")
}

// CanRead reports whether p may read videos of libraryID: the rule the
// detail, segments, subtitles and chapters routes apply, shared with the
// GraphQL layer for videos it reaches through another resource.
func CanRead(p *principal.Principal, libraryID string) bool {
	return p != nil && (p.AccessAllLibraries || p.HasLibrary(libraryID))
}

// canWrite: writes are admin-only (Story 10.13 v1).
func (h *Handler) canWrite(ctx context.Context, _ string) *httperror.Error {
	p := principal.FromContext(ctx)
	if p == nil {
		return httperror.Forbidden("", "authentication required")
	}
	if p.IsAdmin {
		return nil
	}
	return httperror.Forbidden("", "")
}

// replaceTags is the "flat tags array replaces the set" semantic
// (Story 7.4 AC-3 trail-off). Reuses Story 7.14's tag table.
func (h *Handler) replaceTags(ctx context.Context, videoID string, tags []string) error {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM video_tags WHERE video_id = $1`, videoID); err != nil {
		return err
	}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		tagID := uuid.NewString()
		norm := strings.ToLower(t)
		// UPSERT the tag, recover its id.
		_, err := tx.ExecContext(ctx, `
			INSERT INTO tags (id, name, name_norm) VALUES ($1, $2, $3)
			ON CONFLICT (name_norm) DO NOTHING
		`, tagID, t, norm)
		if err != nil {
			return err
		}
		var realID string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE name_norm = $1`, norm).Scan(&realID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO video_tags (video_id, tag_id) VALUES ($1, $2)
			ON CONFLICT (video_id, tag_id) DO NOTHING
		`, videoID, realID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}

// rowScanner is the common surface of *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(...any) error
}

func scanVideoRow(rs rowScanner) (Video, error) {
	var v Video
	var meta []byte
	if err := rs.Scan(
		&v.ID, &v.LibraryID, &v.Path, &v.Filename,
		&v.Title, &v.Description, &v.State,
		&v.DetectedLang, &v.DurationSec, &v.SizeBytes, &v.ContentHash,
		&meta, &v.CreatedAt, &v.UpdatedAt,
	); err != nil {
		return Video{}, err
	}
	if len(meta) > 0 {
		v.Metadata = meta
	}
	return v, nil
}

// pidx and placeholder build $N parameter placeholders for both
// Postgres and SQLite (lib/pq uses $1, go-sqlite3 accepts $1 too).
func pidx(n int) string {
	return "$" + itoa(n)
}
func placeholder(prefix string, n int) string {
	return prefix + pidx(n)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 0 {
		return "-" + itoa(-i)
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
