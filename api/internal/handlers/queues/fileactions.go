package queues

// The file-action ledger (Epic 47, file_actions) and the trash:
//
//	GET  /api/queues/file-actions               filters library_id, kind, state,
//	                                            video_id, restorable; keyset paging
//	                                            (cursor, limit); with each library's
//	                                            trash (entries, bytes, next purge)
//	POST /api/queues/file-actions/{id}/restore  queue the row's undo (file_restore)
//
// Restore moves the trashed file back and reverses the DB change. That is
// file work, so the API only queues it: one file_restore job per library
// carries the rows to restore in its payload (action_ids), and a restore
// asked while one is queued or running joins it.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// FileAction is one ledger row.
type FileAction struct {
	ID            string          `json:"id"`
	LibraryID     string          `json:"library_id"`
	LibraryName   string          `json:"library_name"`
	VideoID       *string         `json:"video_id"`
	VideoTitle    *string         `json:"video_title"`
	JobID         *int64          `json:"job_id"`
	Kind          string          `json:"kind"`
	State         string          `json:"state"`
	OldPath       *string         `json:"old_path"`
	NewPath       *string         `json:"new_path"`
	TrashPath     *string         `json:"trash_path"`
	OldSize       *int64          `json:"old_size"`
	NewSize       *int64          `json:"new_size"`
	Verify        json.RawMessage `json:"verify"`
	Error         *string         `json:"error"`
	PurgeAfter    *time.Time      `json:"purge_after"`
	PurgedAt      *time.Time      `json:"purged_at"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	Restorable    bool            `json:"restorable"`
	RestoreQueued bool            `json:"restore_queued"`
}

// LibraryTrash is one library's trash.
type LibraryTrash struct {
	LibraryID   string     `json:"library_id"`
	Name        string     `json:"name"`
	Entries     int        `json:"entries"`
	Bytes       int64      `json:"bytes"`
	NextPurgeAt *time.Time `json:"next_purge_at"`
	LastPurgeAt *time.Time `json:"last_purge_at"`
}

var (
	actionKinds  = map[string]bool{"convert": true, "dedup": true, "youtube_fetch": true, "purge": true, "restore": true}
	actionStates = map[string]bool{"planned": true, "staged": true, "verified": true, "swapped": true, "done": true,
		"rolled_back": true, "failed": true}
)

// restorableSQL is when a row can be undone: it moved a file to the trash,
// finished, and was not purged (pipeline library_mgmt/swap.py restore; only
// convert and dedup register an undo).
const restorableSQL = `(a.state = 'done' AND a.trash_path IS NOT NULL AND a.old_path IS NOT NULL
	AND a.purged_at IS NULL AND a.kind IN ('convert', 'dedup'))`

// restoreQueuedSQL: a live file_restore job of the row's library lists it.
const restoreQueuedSQL = `EXISTS (SELECT 1 FROM processing_jobs j WHERE j.stage = 'file_restore'
	AND j.subject_kind = 'library' AND j.subject_id = a.library_id
	AND j.state IN ('pending','claimed','running','resuming','paused')
	AND COALESCE(j.payload -> 'action_ids', '[]'::jsonb) ? a.id::text)`

const fileActionCols = `a.id::text, a.library_id::text, l.name, a.video_id::text, v.title, a.job_id, a.kind, a.state,
       a.old_path, a.new_path, a.trash_path, a.old_size, a.new_size, a.verify, a.error, a.purge_after,
       a.purged_at, a.created_at, a.updated_at, ` + restorableSQL + `, ` + restoreQueuedSQL + `
  FROM file_actions a
  JOIN libraries l ON l.id = a.library_id
  LEFT JOIN videos v ON v.id = a.video_id`

func scanFileAction(rs rowScanner) (FileAction, error) {
	var a FileAction
	var vid, title, oldP, newP, trash, aerr sql.NullString
	var job, oldS, newS sql.NullInt64
	var verify []byte
	var purgeAfter, purged sql.NullTime
	if err := rs.Scan(&a.ID, &a.LibraryID, &a.LibraryName, &vid, &title, &job, &a.Kind, &a.State, &oldP, &newP,
		&trash, &oldS, &newS, &verify, &aerr, &purgeAfter, &purged, &a.CreatedAt, &a.UpdatedAt, &a.Restorable,
		&a.RestoreQueued); err != nil {
		return a, err
	}
	a.VideoID, a.VideoTitle = nullStrPtr(vid), nullStrPtr(title)
	a.OldPath, a.NewPath, a.TrashPath, a.Error = nullStrPtr(oldP), nullStrPtr(newP), nullStrPtr(trash), nullStrPtr(aerr)
	if job.Valid {
		a.JobID = &job.Int64
	}
	if oldS.Valid {
		a.OldSize = &oldS.Int64
	}
	if newS.Valid {
		a.NewSize = &newS.Int64
	}
	if len(verify) > 0 {
		a.Verify = json.RawMessage(verify)
	} else {
		a.Verify = json.RawMessage("null")
	}
	a.PurgeAfter, a.PurgedAt = nullTimePtr(purgeAfter), nullTimePtr(purged)
	return a, nil
}

func csvParam(s string, allowed map[string]bool) ([]string, bool) {
	if s == "" {
		return nil, true
	}
	out := strings.Split(s, ",")
	for _, v := range out {
		if !allowed[v] {
			return nil, false
		}
	}
	return out, true
}

// FileActions implements GET /api/queues/file-actions.
func (h *Handler) FileActions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lib, vid := q.Get("library_id"), q.Get("video_id")
	if (lib != "" && !isUUID(lib)) || (vid != "" && !isUUID(vid)) {
		httperror.Write(w, r, httperror.BadRequest("library_id and video_id must be uuids"))
		return
	}
	kinds, ok := csvParam(q.Get("kind"), actionKinds)
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("kind must be convert, dedup, youtube_fetch, purge or restore"))
		return
	}
	states, ok := csvParam(q.Get("state"), actionStates)
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("unknown state"))
		return
	}
	restorable := q.Get("restorable") == "true"
	limit, ok := parseLimit(q.Get("limit"), 50, 200)
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("limit must be 1..200"))
		return
	}
	curAt, curID, ok := parseKeyset(q.Get("cursor"))
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("bad cursor"))
		return
	}
	ctx := r.Context()
	var kindArg, stateArg any
	if kinds != nil {
		kindArg = pq.Array(kinds)
	}
	if states != nil {
		stateArg = pq.Array(states)
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT `+fileActionCols+`
 WHERE ($1::uuid IS NULL OR a.library_id = $1) AND ($2::uuid IS NULL OR a.video_id = $2)
   AND ($3::text[] IS NULL OR a.kind = ANY($3)) AND ($4::text[] IS NULL OR a.state = ANY($4))
   AND (NOT $5 OR `+restorableSQL+`)
   AND ($6::timestamptz IS NULL OR (a.created_at, a.id::text) < ($6, $7))
 ORDER BY a.created_at DESC, a.id::text DESC LIMIT $8`,
		nullIfEmpty(lib), nullIfEmpty(vid), kindArg, stateArg, restorable, curAt, curID, limit+1)
	if err != nil {
		writeErr(w, r, "file actions", err)
		return
	}
	defer rows.Close()
	items := []FileAction{}
	for rows.Next() {
		a, err := scanFileAction(rows)
		if err != nil {
			writeErr(w, r, "file actions", err)
			return
		}
		items = append(items, a)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, r, "file actions", err)
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		c := makeKeyset(last.CreatedAt, last.ID)
		next = &c
	}
	trash, err := h.libraryTrash(ctx, lib)
	if err != nil {
		writeErr(w, r, "trash", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items, "next": next, "trash": trash})
}

// libraryTrash is the trash of every library that has one (or of lib).
func (h *Handler) libraryTrash(ctx context.Context, lib string) ([]LibraryTrash, error) {
	rows, err := h.DB.QueryContext(ctx, `
SELECT l.id::text, l.name, count(a.id), COALESCE(sum(a.old_size), 0)::bigint, min(a.purge_after),
       (SELECT max(j.finished_at) FROM processing_jobs j WHERE j.stage = 'trash_purge'
         AND j.subject_kind = 'library' AND j.subject_id = l.id AND j.state = 'done')
  FROM libraries l
  JOIN file_actions a ON a.library_id = l.id AND a.state = 'done' AND a.trash_path IS NOT NULL AND a.purged_at IS NULL
 WHERE $1::uuid IS NULL OR l.id = $1
 GROUP BY l.id, l.name ORDER BY l.name, l.id`, nullIfEmpty(lib))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LibraryTrash{}
	for rows.Next() {
		var t LibraryTrash
		var next, last sql.NullTime
		if err := rows.Scan(&t.LibraryID, &t.Name, &t.Entries, &t.Bytes, &next, &last); err != nil {
			return nil, err
		}
		t.NextPurgeAt, t.LastPurgeAt = nullTimePtr(next), nullTimePtr(last)
		out = append(out, t)
	}
	return out, rows.Err()
}

// mergeActionIDsSQL adds the rows to restore to the job the enqueue landed on
// (the enqueue keeps an existing row's payload; a running row hands its payload
// to its follow-up, whose restore of a row already restored is a no-op).
const mergeActionIDsSQL = `
UPDATE processing_jobs
   SET payload = COALESCE(payload, '{}'::jsonb)
       || jsonb_build_object('action_ids', (
            SELECT COALESCE(jsonb_agg(DISTINCT x), '[]'::jsonb)
              FROM jsonb_array_elements(COALESCE(payload -> 'action_ids', '[]'::jsonb) || $2::jsonb) AS x))
 WHERE id = $1`

// RestoreResponse is the queued restore.
type RestoreResponse struct {
	ActionID string `json:"action_id"`
	JobID    *int64 `json:"job_id"`
	Outcome  string `json:"outcome"`
}

// RestoreFileAction implements POST /api/queues/file-actions/{id}/restore.
func (h *Handler) RestoreFileAction(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isUUID(id) {
		httperror.Write(w, r, httperror.BadRequest("file action id must be a uuid"))
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		writeErr(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	a, err := scanFileAction(tx.QueryRowContext(ctx, `SELECT `+fileActionCols+` WHERE a.id = $1 FOR UPDATE OF a`, id))
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("file action not found"))
		return
	}
	if err != nil {
		writeErr(w, r, "file action", err)
		return
	}
	if !a.Restorable {
		why := "only a finished conversion or merge whose file is still in the trash can be restored"
		if a.PurgedAt != nil {
			why = "its file was purged from the trash"
		}
		httperror.Write(w, r, httperror.Conflict("", why).With("state", a.State).With("kind", a.Kind))
		return
	}
	res, err := Enqueue(ctx, tx, EnqueueParams{SubjectKind: KindLibrary, SubjectID: a.LibraryID,
		Stage: StageFileRestore, Priority: prio(StageFileRestore), Force: true,
		Reason:  ReasonFor(ctx, "restore from trash", map[string]any{"file_action": id}),
		Payload: map[string]any{"action_ids": []string{id}}})
	if err != nil {
		writeErr(w, r, "enqueue", err)
		return
	}
	if !res.Accepted() {
		httperror.Write(w, r, httperror.Conflict("", "the restore queue does not take it: "+res.Outcome).
			With("outcome", res.Outcome))
		return
	}
	if _, err := tx.ExecContext(ctx, mergeActionIDsSQL, *res.JobID, `["`+id+`"]`); err != nil {
		writeErr(w, r, "enqueue", err)
		return
	}
	if err := auditAdmin(ctx, tx, "queue.file_action.restore", "file_action", id, map[string]any{
		"library_id": a.LibraryID, "kind": a.Kind, "video_id": a.VideoID, "trash_path": a.TrashPath,
		"old_path": a.OldPath, "job_id": res.JobID, "outcome": res.Outcome,
	}); err != nil {
		writeErr(w, r, "audit", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, r, "commit", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, RestoreResponse{ActionID: id, JobID: res.JobID, Outcome: res.Outcome})
}
