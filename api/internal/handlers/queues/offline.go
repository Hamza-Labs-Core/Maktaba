package queues

// Epic 47 (story 47.11): keep libraries offline, in MP4 and without
// duplicates. The Queues page's library actions:
//
//	POST /api/libraries/{id}/convert             {dry_run?, all?}          convert_mp4 (library)
//	POST /api/libraries/{id}/dedup-scan          {dry_run?, all?}          dedup_scan (library)
//	POST /api/libraries/{id}/youtube/refresh     {all?, full_listing?, refresh?}  youtube_sync, forced
//	POST /api/libraries/{id}/youtube/backfill    {}                        youtube_backfill (resume)
//	POST /api/libraries/{id}/youtube/retry       {states?, source_id?}     wanted again + fetch/backfill
//	POST /api/libraries/{id}/youtube/tartube     {path, database?, channels?}  youtube_sync seeds from Tartube
//
// Every one is admin only (the package's Mount), audited, and only
// enqueues through processing_jobs_enqueue: the API never runs pipeline
// work itself. "all" means every library that is not deleted.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Stages of Epic 47 (pipeline engine/graph.py).
const (
	StageConvertMP4      = "convert_mp4"
	StageDedupScan       = "dedup_scan"
	StageDedupApply      = "dedup_apply"
	StageTrashPurge      = "trash_purge"
	StageFileRestore     = "file_restore"
	StageYouTubeSync     = "youtube_sync"
	StageYouTubeFetch    = "youtube_fetch"
	StageYouTubeBackfill = "youtube_backfill"
)

// Epic47Priorities are the enqueue priorities of the Epic 47 stages: the
// pipeline's graph.EPIC47_PRIORITIES, which TestEpic47PrioritiesMatchThePipeline
// keeps equal. A refresh someone asks for runs at the reconciler's
// SOURCE_REFRESH_PRIORITY (110), ahead of the back catalogue that shares its pool.
var Epic47Priorities = map[string]int{
	"convert_mp4":      150,
	"convert_mp4_now":  50,
	"dedup_scan":       150,
	"dedup_apply":      150,
	"youtube_fetch":    120,
	"youtube_backfill": 300,
	"trash_purge":      200,
	"file_restore":     50,
}

// SourceRefreshPriority is youtube_sync's priority for a library that downloads
// (pipeline engine/reconcile.py SOURCE_REFRESH_PRIORITY).
const SourceRefreshPriority = 110

func prio(stage string) *int {
	p, ok := Epic47Priorities[stage]
	if !ok {
		return nil
	}
	return &p
}

// LibraryActionResult is one library's enqueue outcome.
type LibraryActionResult struct {
	LibraryID string `json:"library_id"`
	JobID     *int64 `json:"job_id"`
	Outcome   string `json:"outcome"`
}

// LibraryActionResponse answers every library action.
type LibraryActionResponse struct {
	Stage     string                `json:"stage"`
	Results   []LibraryActionResult `json:"results"`
	Enqueued  int                   `json:"enqueued"`
	Coalesced int                   `json:"coalesced"`
	Skipped   int                   `json:"skipped"`
}

// setPayloadSQL writes a request's payload keys onto the job its enqueue
// landed on. processing_jobs_enqueue keeps an existing row's payload (it
// merges only "seasons"), so without this a dry run, a full listing or a
// Tartube path asked for while a job was already queued would be dropped. A
// running row hands its payload to its follow-up (propagate.enqueue_follow_up).
// The last ask wins.
const setPayloadSQL = `UPDATE processing_jobs SET payload = COALESCE(payload, '{}'::jsonb) || $2::jsonb WHERE id = $1`

// libraryEnqueue is one library action: the stage, its priority and payload.
type libraryEnqueue struct {
	stage    string
	priority *int
	force    bool
	payload  map[string]any
	why      string
}

// enqueueForLibraries enqueues e for each library in one transaction, writes the
// payload onto the job each landed on, and audits the request.
func (h *Handler) enqueueForLibraries(ctx context.Context, libs []string, e libraryEnqueue,
	audit string, auditExtra map[string]any) (LibraryActionResponse, error) {
	out := LibraryActionResponse{Stage: e.stage, Results: []LibraryActionResult{}}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	var payload []byte
	if len(e.payload) > 0 {
		if payload, err = json.Marshal(e.payload); err != nil {
			return out, err
		}
	}
	for _, lib := range libs {
		res, err := Enqueue(ctx, tx, EnqueueParams{
			SubjectKind: KindLibrary, SubjectID: lib, Stage: e.stage, Priority: e.priority,
			Force: e.force, Reason: ReasonFor(ctx, e.why, nil), Payload: e.payload,
		})
		if err != nil {
			return out, err
		}
		if res.JobID != nil && payload != nil {
			if _, err := tx.ExecContext(ctx, setPayloadSQL, *res.JobID, string(payload)); err != nil {
				return out, err
			}
		}
		switch res.Outcome {
		case OutcomeInserted:
			out.Enqueued++
		case OutcomeCoalesced, OutcomeRerunRequested:
			out.Coalesced++
		default:
			out.Skipped++
		}
		out.Results = append(out.Results, LibraryActionResult{LibraryID: lib, JobID: res.JobID, Outcome: res.Outcome})
	}
	extra := map[string]any{"stage": e.stage, "libraries": len(libs), "enqueued": out.Enqueued,
		"coalesced": out.Coalesced, "skipped": out.Skipped}
	if e.payload != nil {
		extra["payload"] = e.payload
	}
	for k, v := range auditExtra {
		extra[k] = v
	}
	target := ""
	if len(libs) == 1 {
		target = libs[0]
	}
	if err := auditAdmin(ctx, tx, audit, "library", target, extra); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// auditAdmin writes one admin action to audit_log: who, what, on which target.
func auditAdmin(ctx context.Context, q interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}, action, targetKind, targetID string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["target_kind"] = targetKind
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var actor, target any
	if p := principal.FromContext(ctx); p != nil {
		if _, err := uuid.Parse(p.UserID); err == nil {
			actor = p.UserID
		}
	}
	if targetID != "" {
		target = targetID
	}
	_, err = q.ExecContext(ctx, `INSERT INTO audit_log (category, action, actor_user_id, target_id, payload)
		VALUES ('admin', $1, $2, $3, $4)`, action, actor, target, string(b))
	return err
}

// readOptionalJSON decodes a body that may be empty (all its fields optional).
func readOptionalJSON(r *http.Request, v any) *httperror.Error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	e := common.ReadJSON(r, v, 0)
	if e != nil && e.Detail == "request body is empty" {
		return nil
	}
	return e
}

var errLibraryNotFound = errors.New("library not found")

// targetLibraries is {id} alone, or every library that is not deleted when all.
func (h *Handler) targetLibraries(ctx context.Context, id string, all bool) ([]string, error) {
	if all {
		rows, err := h.DB.QueryContext(ctx, `SELECT id::text FROM libraries WHERE deleted_at IS NULL ORDER BY name, id`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, rows.Err()
	}
	if err := h.libraryExists(ctx, id); err != nil {
		return nil, err
	}
	return []string{id}, nil
}

func (h *Handler) libraryExists(ctx context.Context, id string) error {
	var ok bool
	err := h.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM libraries WHERE id = $1 AND deleted_at IS NULL)`, id).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return errLibraryNotFound
	}
	return nil
}

// libraryParam reads {id}; all=true accepts the literal "all" in its place.
func libraryParam(w http.ResponseWriter, r *http.Request, all bool) (string, bool) {
	id := chi.URLParam(r, "id")
	if all && id == "all" {
		return id, true
	}
	if !isUUID(id) {
		httperror.Write(w, r, httperror.BadRequest("library id must be a uuid"))
		return "", false
	}
	return id, true
}

func (h *Handler) writeLibraryAction(w http.ResponseWriter, r *http.Request, id string, all bool, e libraryEnqueue,
	audit string, extra map[string]any) {
	ctx := r.Context()
	libs, err := h.targetLibraries(ctx, id, all)
	if errors.Is(err, errLibraryNotFound) {
		httperror.Write(w, r, httperror.NotFound("library not found"))
		return
	}
	if err != nil {
		writeErr(w, r, "libraries", err)
		return
	}
	if all {
		extra = mergeMaps(extra, map[string]any{"all": true})
	}
	out, err := h.enqueueForLibraries(ctx, libs, e, audit, extra)
	if err != nil {
		writeErr(w, r, "enqueue", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

func mergeMaps(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// LibraryRunRequest is the body of convert and dedup-scan.
type LibraryRunRequest struct {
	DryRun bool `json:"dry_run"`
	All    bool `json:"all"`
}

// ConvertLibrary implements POST /api/libraries/{id}/convert: one
// convert_mp4 job per library plans every live video and queues a
// conversion per file that needs one (or, with dry_run, writes the plan).
func (h *Handler) ConvertLibrary(w http.ResponseWriter, r *http.Request) {
	var req LibraryRunRequest
	if e := readOptionalJSON(r, &req); e != nil {
		httperror.Write(w, r, e)
		return
	}
	id, ok := libraryParam(w, r, req.All)
	if !ok {
		return
	}
	h.writeLibraryAction(w, r, id, req.All, libraryEnqueue{
		stage: StageConvertMP4, priority: prio(StageConvertMP4), force: true,
		payload: map[string]any{"dry_run": req.DryRun}, why: "convert to mp4",
	}, "queue.library.convert", nil)
}

// DedupScanLibrary implements POST /api/libraries/{id}/dedup-scan: the
// library's dedup_scan fans out one check per converted video (and a
// conversion for the rest; each video's check follows its conversion).
// all: every library, each compared with every other (scope "all").
func (h *Handler) DedupScanLibrary(w http.ResponseWriter, r *http.Request) {
	var req LibraryRunRequest
	if e := readOptionalJSON(r, &req); e != nil {
		httperror.Write(w, r, e)
		return
	}
	id, ok := libraryParam(w, r, req.All)
	if !ok {
		return
	}
	payload := map[string]any{"dry_run": req.DryRun}
	if req.All {
		payload["scope"] = "all"
	}
	h.writeLibraryAction(w, r, id, req.All, libraryEnqueue{
		stage: StageDedupScan, priority: prio(StageDedupScan), force: true,
		payload: payload, why: "find duplicates",
	}, "queue.library.dedup_scan", nil)
}

// RefreshRequest is the body of youtube/refresh.
type RefreshRequest struct {
	All         bool `json:"all"`
	FullListing bool `json:"full_listing"`
	Refresh     bool `json:"refresh"`
}

// RefreshYouTube implements POST /api/libraries/{id}/youtube/refresh: a
// forced youtube_sync lists the library's sources now and queues what is new.
func (h *Handler) RefreshYouTube(w http.ResponseWriter, r *http.Request) {
	var req RefreshRequest
	if e := readOptionalJSON(r, &req); e != nil {
		httperror.Write(w, r, e)
		return
	}
	id, ok := libraryParam(w, r, req.All)
	if !ok {
		return
	}
	p := SourceRefreshPriority
	h.writeLibraryAction(w, r, id, req.All, libraryEnqueue{
		stage: StageYouTubeSync, priority: &p, force: true,
		payload: map[string]any{"full_listing": req.FullListing, "refresh": req.Refresh},
		why:     "refresh from youtube",
	}, "queue.library.youtube_refresh", nil)
}

// ResumeBackfill implements POST /api/libraries/{id}/youtube/backfill: the
// back catalogue carries on where it stopped (its items stayed wanted).
func (h *Handler) ResumeBackfill(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	if e := readOptionalJSON(r, &req); e != nil {
		httperror.Write(w, r, e)
		return
	}
	id, ok := libraryParam(w, r, false)
	if !ok {
		return
	}
	h.writeLibraryAction(w, r, id, false, libraryEnqueue{
		stage: StageYouTubeBackfill, priority: prio(StageYouTubeBackfill), why: "resume backfill",
	}, "queue.library.youtube_backfill", nil)
}

// RetryDownloadsRequest is the body of youtube/retry.
type RetryDownloadsRequest struct {
	// States are the youtube_downloads states to want again (default all three).
	States   []string `json:"states"`
	SourceID string   `json:"source_id"`
}

var retryableDownloadStates = map[string]bool{"gone": true, "skipped": true, "failed": true}

// RetryDownloadsResponse says how many items are wanted again and what was queued.
type RetryDownloadsResponse struct {
	Wanted  int                   `json:"wanted"`
	New     int                   `json:"new"`
	Backlog int                   `json:"backfill"`
	Results []LibraryActionResult `json:"results"`
}

const retryDownloadsSQL = `
UPDATE youtube_downloads
   SET state = 'wanted', reason = NULL, failure_class = NULL, error = NULL, not_before = NULL, attempts = 0
 WHERE library_id = $1 AND state = ANY($2::text[]) AND ($3::uuid IS NULL OR source_id = $3)
RETURNING backfill`

// RetryDownloads implements POST /api/libraries/{id}/youtube/retry: the
// library's gone, skipped or failed downloads are wanted again, and the
// download (new uploads) and back-catalogue stages are queued for them.
func (h *Handler) RetryDownloads(w http.ResponseWriter, r *http.Request) {
	var req RetryDownloadsRequest
	if e := readOptionalJSON(r, &req); e != nil {
		httperror.Write(w, r, e)
		return
	}
	id, ok := libraryParam(w, r, false)
	if !ok {
		return
	}
	states := req.States
	if len(states) == 0 {
		states = []string{"gone", "skipped", "failed"}
	}
	for _, s := range states {
		if !retryableDownloadStates[s] {
			httperror.Write(w, r, httperror.BadRequest("states must be gone, skipped or failed"))
			return
		}
	}
	var source any
	if req.SourceID != "" {
		if !isUUID(req.SourceID) {
			httperror.Write(w, r, httperror.BadRequest("source_id must be a uuid"))
			return
		}
		source = req.SourceID
	}
	ctx := r.Context()
	if err := h.libraryExists(ctx, id); errors.Is(err, errLibraryNotFound) {
		httperror.Write(w, r, httperror.NotFound("library not found"))
		return
	} else if err != nil {
		writeErr(w, r, "library", err)
		return
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		writeErr(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, retryDownloadsSQL, id, pq.Array(states), source)
	if err != nil {
		writeErr(w, r, "retry downloads", err)
		return
	}
	out := RetryDownloadsResponse{Results: []LibraryActionResult{}}
	for rows.Next() {
		var backfill bool
		if err := rows.Scan(&backfill); err != nil {
			rows.Close()
			writeErr(w, r, "retry downloads", err)
			return
		}
		out.Wanted++
		if backfill {
			out.Backlog++
		} else {
			out.New++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeErr(w, r, "retry downloads", err)
		return
	}
	for _, s := range []struct {
		stage string
		n     int
	}{{StageYouTubeFetch, out.New}, {StageYouTubeBackfill, out.Backlog}} {
		if s.n == 0 {
			continue
		}
		res, err := Enqueue(ctx, tx, EnqueueParams{SubjectKind: KindLibrary, SubjectID: id, Stage: s.stage,
			Priority: prio(s.stage), Reason: ReasonFor(ctx, "retry downloads", map[string]any{"items": s.n})})
		if err != nil {
			writeErr(w, r, "enqueue", err)
			return
		}
		out.Results = append(out.Results, LibraryActionResult{LibraryID: id, JobID: res.JobID, Outcome: res.Outcome})
	}
	if err := auditAdmin(ctx, tx, "queue.library.youtube_retry", "library", id, map[string]any{
		"states": states, "source_id": source, "wanted": out.Wanted, "new": out.New, "backfill": out.Backlog,
	}); err != nil {
		writeErr(w, r, "audit", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, r, "commit", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// TartubeRequest is the body of youtube/tartube: a Tartube database on the
// home (its JSON export first; tartube.db through the stub-only reader).
type TartubeRequest struct {
	Path     string   `json:"path"`
	Database string   `json:"database"`
	Channels []string `json:"channels"`
}

var tartubeExt = regexp.MustCompile(`(?i)\.(json|db)$`)

func (t TartubeRequest) validate() *httperror.Error {
	var errs []httperror.FieldError
	if !filepath.IsAbs(t.Path) || !tartubeExt.MatchString(t.Path) || strings.ContainsRune(t.Path, 0) {
		errs = append(errs, httperror.FieldError{Field: "path", Message: "an absolute path to Tartube's .json export or its .db"})
	}
	if t.Database != "" && (!filepath.IsAbs(t.Database) || !strings.HasSuffix(strings.ToLower(t.Database), ".db")) {
		errs = append(errs, httperror.FieldError{Field: "database", Message: "an absolute path to tartube.db"})
	}
	if len(t.Channels) > 500 {
		errs = append(errs, httperror.FieldError{Field: "channels", Message: "at most 500"})
	}
	for _, c := range t.Channels {
		if strings.TrimSpace(c) == "" || len(c) > 300 {
			errs = append(errs, httperror.FieldError{Field: "channels", Message: "each a name or URL part of 1 to 300 characters"})
			break
		}
	}
	if errs != nil {
		return httperror.Unprocessable(errs)
	}
	return nil
}

// ImportTartube implements POST /api/libraries/{id}/youtube/tartube: a forced
// youtube_sync seeds the library's downloads (done) from the Tartube database
// before it lists, so the back catalogue never downloads what Tartube has.
func (h *Handler) ImportTartube(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryParam(w, r, false)
	if !ok {
		return
	}
	var req TartubeRequest
	if e := common.ReadJSON(r, &req, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if e := req.validate(); e != nil {
		httperror.Write(w, r, e)
		return
	}
	tartube := map[string]any{"path": req.Path}
	if req.Database != "" {
		tartube["database"] = req.Database
	}
	if len(req.Channels) > 0 {
		tartube["channels"] = req.Channels
	}
	p := SourceRefreshPriority
	h.writeLibraryAction(w, r, id, false, libraryEnqueue{
		stage: StageYouTubeSync, priority: &p, force: true,
		payload: map[string]any{"tartube": tartube}, why: "import tartube",
	}, "queue.library.youtube_tartube", nil)
}
