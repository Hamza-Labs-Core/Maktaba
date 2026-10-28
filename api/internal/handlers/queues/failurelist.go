package queues

import (
	"context"
	"database/sql"

	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Epic 40: one list of every failing item, all stages and classes.
//
//	GET  /api/queues/failures          filters: stage, class, signature,
//	                                   library, path_prefix, status
//	                                   (needs_you | failing | waiting |
//	                                   retrying | ignored | all; default:
//	                                   everything not ignored); before, limit
//	POST /api/queues/failures/actions  admin: {action, job_ids | filter, ...}
//
// An item is a subject and stage whose latest job failed, is parked on a
// dependency, or is pending a retry after a failure.

// Item statuses.
const (
	StatusNeedsYou = "needs_you"
	StatusFailing  = "failing"
	StatusWaiting  = "waiting"
	StatusRetrying = "retrying"
	StatusIgnored  = "ignored"
)

var itemStatuses = map[string]bool{StatusNeedsYou: true, StatusFailing: true, StatusWaiting: true,
	StatusRetrying: true, StatusIgnored: true, "all": true}

// FailureItem is one row of the failures page.
type FailureItem struct {
	JobID          int64      `json:"job_id"`
	Stage          string     `json:"stage"`
	SubjectKind    string     `json:"subject_kind"`
	SubjectID      string     `json:"subject_id"`
	Label          string     `json:"label"`
	Path           *string    `json:"path"`
	LibraryID      *string    `json:"library_id"`
	LibraryName    *string    `json:"library_name"`
	State          string     `json:"state"`
	Status         string     `json:"status"`
	Message        string     `json:"message"`
	Signature      *string    `json:"signature"`
	Class          *string    `json:"class"`
	ParkedOn       *string    `json:"parked_on"`
	Reason         string     `json:"reason"`
	Attempts       int        `json:"attempts"`
	MaxAttempts    int        `json:"max_attempts"`
	FirstFailedAt  *time.Time `json:"first_failed_at"`
	LastFailedAt   *time.Time `json:"last_failed_at"`
	MissingConfirm *time.Time `json:"missing_confirmed_at"`
	YouTubeID      *string    `json:"youtube_id"`
	Check          *ItemCheck `json:"access_check"`
	// FileAction is the ledger row the failed job wrote (Epic 47): a failed
	// conversion's measured check, or a row the file-actions page can restore.
	FileAction *ItemFileAction `json:"file_action"`
}

// ItemFileAction is the latest file_actions row of an item's job.
type ItemFileAction struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	State      string `json:"state"`
	Restorable bool   `json:"restorable"`
}

// ItemCheck is the last access check of a video's file (item_checks).
type ItemCheck struct {
	OK        bool      `json:"ok"`
	Error     *string   `json:"error"`
	Host      *string   `json:"host"`
	CheckedAt time.Time `json:"checked_at"`
}

// FacetCount is one value of a filter with its count.
type FacetCount struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
	Count int    `json:"count"`
}

// FailureFilter is the list's filter (query string, or an action's body).
type FailureFilter struct {
	Stage      string `json:"stage"`
	Class      string `json:"class"`
	Signature  string `json:"signature"`
	Library    string `json:"library"`
	PathPrefix string `json:"path_prefix"`
	Status     string `json:"status"`
}

func (f FailureFilter) validate() *httperror.Error {
	if f.Status != "" && !itemStatuses[f.Status] {
		return httperror.BadRequest("status must be needs_you, failing, waiting, retrying, ignored or all")
	}
	if f.Library != "" && !isUUID(f.Library) {
		return httperror.BadRequest("library must be a uuid")
	}
	return nil
}

// openItemsCTE: $1 is the stage+signature pairs (stage || chr(31) ||
// signature) that make their stage red right now.
const openItemsCTE = `
WITH f AS (
    SELECT DISTINCT subject_kind, subject_id, stage FROM processing_jobs
     WHERE state = 'failed' OR (state = 'pending' AND (parked_on IS NOT NULL OR error IS NOT NULL))
), latest AS (
    SELECT DISTINCT ON (j.subject_kind, j.subject_id, j.stage)
           j.id, j.stage, j.subject_kind, j.subject_id, j.state, j.library_id, j.attempts,
           j.max_attempts, j.error, j.error_cause, j.failure_class, j.parked_on,
           j.finished_at, j.created_at
      FROM processing_jobs j
      JOIN f ON f.subject_kind = j.subject_kind AND f.subject_id = j.subject_id AND f.stage = j.stage
     ORDER BY j.subject_kind, j.subject_id, j.stage, j.id DESC
), items AS (
    SELECT l.*, v.path AS path, v.missing_confirmed_at AS missing_confirmed_at,
           COALESCE(l.library_id, v.library_id) AS lib_id,
           CASE WHEN fi.subject_id IS NOT NULL THEN 'ignored'
                WHEN l.parked_on IS NOT NULL THEN 'waiting'
                WHEN l.state = 'pending' THEN 'retrying'
                WHEN (l.stage || chr(31) || COALESCE(l.error_cause, '')) = ANY($1::text[]) THEN 'failing'
                ELSE 'needs_you' END AS status
      FROM latest l
      LEFT JOIN videos v ON l.subject_kind = 'video' AND v.id = l.subject_id
      LEFT JOIN failure_ignores fi ON fi.subject_kind = l.subject_kind
           AND fi.subject_id = l.subject_id AND fi.stage = l.stage
     WHERE (l.state = 'failed' OR (l.state = 'pending' AND (l.parked_on IS NOT NULL OR l.error IS NOT NULL)))
       AND NOT (l.subject_kind = 'video' AND v.id IS NULL)
)`

// itemFilterSQL: $2 stage, $3 class (exact or a prefix before ':'), $4
// signature, $5 library, $6 path prefix (LIKE-escaped), $7 status.
const itemFilterSQL = `
   ($2::text = '' OR i.stage = $2::text)
   AND ($3::text = '' OR i.failure_class = $3::text OR i.failure_class LIKE $3::text || ':%'
        OR ($3::text = 'unclassified' AND i.failure_class IS NULL))
   AND ($4::text = '' OR i.error_cause = $4::text)
   AND ($5::text = '' OR i.lib_id = $5::uuid)
   AND ($6::text = '' OR i.path LIKE $6::text ESCAPE '\')
   AND (CASE WHEN $7::text = '' THEN i.status <> 'ignored'
             WHEN $7::text = 'all' THEN true
             ELSE i.status = $7::text END)`

func likePrefix(p string) string {
	if p == "" {
		return ""
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(p) + "%"
}

func (h *Handler) filterArgs(ctx context.Context, f FailureFilter) ([]any, error) {
	failing, err := h.failingPairs(ctx)
	if err != nil {
		return nil, err
	}
	return []any{pq.Array(failing), f.Stage, f.Class, f.Signature, f.Library, likePrefix(f.PathPrefix), f.Status}, nil
}

// failingPairs is stage || chr(31) || signature for every red stage.
func (h *Handler) failingPairs(ctx context.Context) ([]string, error) {
	now := h.now()
	stages, err := h.loadStages(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := h.failureGroups(ctx, "", false, stages, now)
	if err != nil {
		return nil, err
	}
	hd, err := h.loadStageHealth(ctx, now)
	if err != nil {
		return nil, err
	}
	open := openBySignature(groups)
	out := []string{}
	for _, st := range stages {
		sh := stageHealth(healthInputs{Failures: hd.failures[st.Stage], DoneWindow: hd.done[st.Stage],
			Waiting: hd.waiting[st.Stage], FailedOpen: open[st.Stage]}, hd.cfg)
		if sh.Failing != nil {
			out = append(out, st.Stage+"\x1f"+sh.Failing.Signature)
		}
	}
	return out, nil
}

func failureListSQL() string {
	joins, expr := labelSQL("i.subject_kind", "i.subject_id")
	return openItemsCTE + `
SELECT i.id, i.stage, i.subject_kind, i.subject_id::text, ` + expr + `, i.path, i.lib_id::text, lib.name,
       i.state, i.status, i.error, i.error_cause, i.failure_class, i.parked_on, i.attempts, i.max_attempts,
       COALESCE(ev.first_at, i.finished_at, i.created_at), COALESCE(ev.last_at, i.finished_at, i.created_at),
       i.missing_confirmed_at, yt.external_id, ic.ok, ic.error, ic.host, ic.checked_at,
       fa.id::text, fa.kind, fa.state, COALESCE(fa.restorable, false)
  FROM items i
  LEFT JOIN libraries lib ON lib.id = i.lib_id
  LEFT JOIN LATERAL (SELECT min(fe.at) AS first_at, max(fe.at) AS last_at FROM job_events fe
                      WHERE fe.subject_kind = i.subject_kind AND fe.subject_id = i.subject_id
                        AND fe.stage = i.stage AND fe.kind IN ('failed', 'retry_scheduled', 'paused')) ev ON true
  LEFT JOIN LATERAL (SELECT m.external_id FROM media_metadata_enrichment m
                      WHERE i.subject_kind = 'video' AND m.video_id = i.subject_id
                        AND m.provider = 'youtube' AND NOT m.is_dismissed LIMIT 1) yt ON true
  LEFT JOIN item_checks ic ON i.subject_kind = 'video' AND ic.video_id = i.subject_id
  LEFT JOIN LATERAL (SELECT a.id, a.kind, a.state, ` + restorableSQL + ` AS restorable FROM file_actions a
                      WHERE a.job_id = i.id ORDER BY a.created_at DESC LIMIT 1) fa ON true` + joins + `
 WHERE ` + itemFilterSQL + `
   AND ($8::bigint = 0 OR i.id < $8::bigint)
 ORDER BY i.id DESC
 LIMIT $9`
}

// failureFacetsSQL counts the open items by stage, class, signature,
// library and status, over every filter but status (ignored items only
// count under status).
const failureFacetsSQL = openItemsCTE + `
, filtered AS (
    SELECT i.* FROM items i
     WHERE ($2::text = '' OR i.stage = $2::text)
       AND ($3::text = '' OR i.failure_class = $3::text OR i.failure_class LIKE $3::text || ':%'
            OR ($3::text = 'unclassified' AND i.failure_class IS NULL))
       AND ($4::text = '' OR i.error_cause = $4::text)
       AND ($5::text = '' OR i.lib_id = $5::uuid)
       AND ($6::text = '' OR i.path LIKE $6::text ESCAPE '\')
       AND ($7::text = $7::text)
)
SELECT 'stage', stage, '', count(*) FROM filtered WHERE status <> 'ignored' GROUP BY stage
UNION ALL
SELECT 'class', COALESCE(failure_class, 'unclassified'), '', count(*) FROM filtered WHERE status <> 'ignored'
 GROUP BY COALESCE(failure_class, 'unclassified')
UNION ALL
(SELECT 'signature', COALESCE(error_cause, ''), stage, count(*) FROM filtered WHERE status <> 'ignored'
  GROUP BY COALESCE(error_cause, ''), stage ORDER BY count(*) DESC LIMIT 100)
UNION ALL
SELECT 'library', COALESCE(fl.lib_id::text, ''), COALESCE(max(lb.name), ''), count(*) FROM filtered fl
  LEFT JOIN libraries lb ON lb.id = fl.lib_id WHERE fl.status <> 'ignored' GROUP BY fl.lib_id
UNION ALL
SELECT 'status', status, '', count(*) FROM filtered GROUP BY status`

func readFilter(r *http.Request) FailureFilter {
	q := r.URL.Query()
	return FailureFilter{Stage: q.Get("stage"), Class: q.Get("class"), Signature: q.Get("signature"),
		Library: q.Get("library"), PathPrefix: q.Get("path_prefix"), Status: q.Get("status")}
}

// Failures implements GET /api/queues/failures (admin: it lists items of
// every library, with their paths).
func (h *Handler) Failures(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	f := readFilter(r)
	if e := f.validate(); e != nil {
		httperror.Write(w, r, e)
		return
	}
	before, ok := parseCursor(r.URL.Query().Get("before"))
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("before must be a job id"))
		return
	}
	limit := 50
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 200 {
			httperror.Write(w, r, httperror.BadRequest("limit must be 1..200"))
			return
		}
		limit = n
	}
	ctx := r.Context()
	args, err := h.filterArgs(ctx, f)
	if err != nil {
		writeErr(w, r, "failures", err)
		return
	}
	items, err := h.queryItems(ctx, append(args, before, limit+1)...)
	if err != nil {
		writeErr(w, r, "failures", err)
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := strconv.FormatInt(items[len(items)-1].JobID, 10)
		next = &c
	}
	facets, err := h.queryFacets(ctx, args)
	if err != nil {
		writeErr(w, r, "failure facets", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items, "next": next, "facets": facets})
}

func (h *Handler) queryItems(ctx context.Context, args ...any) ([]FailureItem, error) {
	rows, err := h.DB.QueryContext(ctx, failureListSQL(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FailureItem{}
	for rows.Next() {
		var it FailureItem
		var path, lib, libName, errTxt, cause, cls, parked, yt, chkErr, chkHost, faID, faKind, faState sql.NullString
		var faRestorable bool
		var first, last, missing, chkAt sql.NullTime
		var chkOK sql.NullBool
		if err := rows.Scan(&it.JobID, &it.Stage, &it.SubjectKind, &it.SubjectID, &it.Label, &path, &lib,
			&libName, &it.State, &it.Status, &errTxt, &cause, &cls, &parked, &it.Attempts, &it.MaxAttempts,
			&first, &last, &missing, &yt, &chkOK, &chkErr, &chkHost, &chkAt, &faID, &faKind, &faState,
			&faRestorable); err != nil {
			return nil, err
		}
		it.Path, it.LibraryID, it.LibraryName = nullStrPtr(path), nullStrPtr(lib), nullStrPtr(libName)
		it.Message = errorText(errTxt.String)
		it.Signature, it.Class, it.ParkedOn = nullStrPtr(cause), nullStrPtr(cls), nullStrPtr(parked)
		it.FirstFailedAt, it.LastFailedAt, it.MissingConfirm = nullTimePtr(first), nullTimePtr(last), nullTimePtr(missing)
		it.YouTubeID = nullStrPtr(yt)
		if chkOK.Valid && chkAt.Valid {
			it.Check = &ItemCheck{OK: chkOK.Bool, Error: nullStrPtr(chkErr), Host: nullStrPtr(chkHost), CheckedAt: chkAt.Time}
		}
		if faID.Valid {
			it.FileAction = &ItemFileAction{ID: faID.String, Kind: faKind.String, State: faState.String,
				Restorable: faRestorable}
		}
		it.Reason = itemReason(it)
		out = append(out, it)
	}
	return out, rows.Err()
}

func (h *Handler) queryFacets(ctx context.Context, args []any) (map[string][]FacetCount, error) {
	rows, err := h.DB.QueryContext(ctx, failureFacetsSQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]FacetCount{"stage": {}, "class": {}, "signature": {}, "library": {}, "status": {}}
	for rows.Next() {
		var dim string
		var fc FacetCount
		if err := rows.Scan(&dim, &fc.Value, &fc.Label, &fc.Count); err != nil {
			return nil, err
		}
		out[dim] = append(out[dim], fc)
	}
	return out, rows.Err()
}

// itemReason is the why of an item, as a code the web app words:
// what an admin can do follows from it.
func itemReason(it FailureItem) string {
	sig := ""
	if it.Signature != nil {
		sig = *it.Signature
	}
	cls := ""
	if it.Class != nil {
		cls = *it.Class
	}
	switch {
	case it.Status == StatusWaiting && it.ParkedOn != nil:
		return "waiting:" + *it.ParkedOn
	case strings.HasPrefix(sig, "file_missing") && it.MissingConfirm != nil:
		return "missing_confirmed"
	case strings.HasPrefix(sig, "file_missing"):
		return "missing_unconfirmed"
	case strings.HasPrefix(sig, "permission_denied"):
		return "permission"
	case strings.HasPrefix(sig, "ffmpeg_decode"):
		return "damaged"
	case strings.HasPrefix(sig, "youtube_unavailable"):
		return "youtube_gone"
	}
	if r := epic47Reason(sig); r != "" {
		return r
	}
	switch {
	case it.Status == StatusRetrying:
		return "retrying"
	case cls == "transient" && it.State == "failed":
		return "retries_spent"
	case it.Status == StatusFailing:
		return "stage_failing"
	case cls == "item_permanent":
		return "item"
	}
	return "unexplained"
}

// epic47Reasons are the failure kinds of the offline pipelines (Epic 47) by
// their signature's prefix ("kind" or "kind:message"), as reason codes the
// failures page words.
var epic47Reasons = []struct{ prefix, reason string }{
	{"convert_verify", "convert_verify"},
	{"convert_swap", "convert_failed"},
	{"convert_ffmpeg", "convert_failed"},
	{"youtube_rate_limited", "youtube_rate_limited"},
	{"youtube_bot_check", "youtube_bot_check"},
	{"tartube_", "tartube"},
	{"fk_not_covered", "dedup_blocked"},
	{"keeper_unreadable", "dedup_keeper_unreadable"},
	{"trash_move_failed", "trash_move_failed"},
	{"trash_size_mismatch", "trash_changed"},
	{"restore_", "restore_refused"},
	// after three failed downloads in a row with one class (youtube_<class>)
	{"youtube_", "youtube_downloads_failing"},
}

func epic47Reason(sig string) string {
	for _, r := range epic47Reasons {
		if strings.HasPrefix(sig, r.prefix) {
			return r.reason
		}
	}
	return ""
}
