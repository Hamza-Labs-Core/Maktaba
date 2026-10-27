package queues

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Actions of POST /api/queues/failures/actions.
const (
	FixRetry       = "retry"
	FixIgnore      = "ignore"
	FixUnignore    = "unignore"
	FixRescan      = "rescan"
	FixRemove      = "remove"
	FixRedownload  = "redownload"
	FixPath        = "fix_path"
	FixPathMap     = "path_map"
	FixCheckAccess = "check_access"
)

var fixActions = map[string]bool{FixRetry: true, FixIgnore: true, FixUnignore: true, FixRescan: true,
	FixRemove: true, FixRedownload: true, FixPath: true, FixPathMap: true, FixCheckAccess: true}

// maxFixTargets bounds one action (a whole signature group included).
const maxFixTargets = 20000

// FixRequest is the body of POST /api/queues/failures/actions: the
// items are job_ids (each the latest job of its item) or a filter.
type FixRequest struct {
	Action string         `json:"action"`
	JobIDs []int64        `json:"job_ids"`
	Filter *FailureFilter `json:"filter"`
	// Path is fix_path's new stored path of the one video selected.
	Path string `json:"path"`
	// PathMap is path_map's rule: a share prefix and this machine's mount.
	PathMap *struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"path_map"`
}

// FixResult says what an action touched.
type FixResult struct {
	Action   string `json:"action"`
	Matched  int    `json:"matched"`
	Affected int    `json:"affected"`
	Skipped  int    `json:"skipped"`
}

type fixTarget struct {
	jobID              int64
	stage, kind, id    string
	state, status      string
	parkedOn, lib, pth sql.NullString
}

const fixTargetCols = `
SELECT i.id, i.stage, i.subject_kind, i.subject_id::text, i.state, i.status, i.parked_on,
       i.lib_id::text, i.path
  FROM items i`

func (h *Handler) fixTargets(ctx context.Context, req FixRequest) ([]fixTarget, error) {
	var rows *sql.Rows
	var err error
	if len(req.JobIDs) > 0 {
		failing, ferr := h.failingPairs(ctx)
		if ferr != nil {
			return nil, ferr
		}
		rows, err = h.DB.QueryContext(ctx, openItemsCTE+fixTargetCols+`
 WHERE i.id = ANY($2::bigint[]) ORDER BY i.id`, pq.Array(failing), pq.Array(req.JobIDs))
	} else {
		args, aerr := h.filterArgs(ctx, *req.Filter)
		if aerr != nil {
			return nil, aerr
		}
		rows, err = h.DB.QueryContext(ctx, openItemsCTE+fixTargetCols+`
 WHERE `+itemFilterSQL+` ORDER BY i.id LIMIT 20000`, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []fixTarget
	for rows.Next() {
		var t fixTarget
		if err := rows.Scan(&t.jobID, &t.stage, &t.kind, &t.id, &t.state, &t.status, &t.parkedOn, &t.lib, &t.pth); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// FailureActions implements POST /api/queues/failures/actions.
func (h *Handler) FailureActions(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	var req FixRequest
	if e := common.ReadJSON(r, &req, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if !fixActions[req.Action] {
		httperror.Write(w, r, httperror.BadRequest("unknown action"))
		return
	}
	if (len(req.JobIDs) == 0) == (req.Filter == nil) {
		httperror.Write(w, r, httperror.BadRequest("one of job_ids or filter is required"))
		return
	}
	if len(req.JobIDs) > maxFixTargets {
		httperror.Write(w, r, httperror.BadRequest("too many job_ids"))
		return
	}
	if req.Filter != nil {
		if e := req.Filter.validate(); e != nil {
			httperror.Write(w, r, e)
			return
		}
	}
	switch req.Action {
	case FixPath:
		if strings.TrimSpace(req.Path) == "" || len(req.JobIDs) != 1 {
			httperror.Write(w, r, httperror.BadRequest("fix_path takes one job_id and a path"))
			return
		}
	case FixPathMap:
		if req.PathMap == nil || strings.TrimSpace(req.PathMap.From) == "" || strings.TrimSpace(req.PathMap.To) == "" {
			httperror.Write(w, r, httperror.BadRequest("path_map takes {from, to}"))
			return
		}
	}
	ctx := r.Context()
	targets, err := h.fixTargets(ctx, req)
	if err != nil {
		writeErr(w, r, "find items", err)
		return
	}
	res := FixResult{Action: req.Action, Matched: len(targets)}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		writeErr(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	if e := h.applyFix(ctx, tx, req, targets, &res); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if err := auditFix(ctx, tx, req, res, targets); err != nil {
		writeErr(w, r, "audit", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, r, "commit", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, res)
}

func by(ctx context.Context) string {
	if p := principal.FromContext(ctx); p != nil && p.UserID != "" {
		return "user:" + p.UserID
	}
	return "system"
}

func (h *Handler) applyFix(ctx context.Context, tx *sql.Tx, req FixRequest, ts []fixTarget, res *FixResult) *httperror.Error {
	internal := func(what string, err error) *httperror.Error {
		return httperror.Internal(what).Wrap(err)
	}
	videos := func() []fixTarget {
		out := []fixTarget{}
		for _, t := range ts {
			if t.kind == KindVideo {
				out = append(out, t)
			}
		}
		return out
	}
	switch req.Action {
	case FixRetry:
		n, err := retryTargets(ctx, tx, ts)
		if err != nil {
			return internal("retry", err)
		}
		res.Affected = n
	case FixIgnore, FixUnignore:
		for _, t := range ts {
			var r sql.Result
			var err error
			if req.Action == FixIgnore {
				r, err = tx.ExecContext(ctx, `INSERT INTO failure_ignores (subject_kind, subject_id, stage, ignored_by)
					VALUES ($1, $2::uuid, $3, $4) ON CONFLICT DO NOTHING`, t.kind, t.id, t.stage, by(ctx))
			} else {
				r, err = tx.ExecContext(ctx, `DELETE FROM failure_ignores
					WHERE subject_kind = $1 AND subject_id = $2::uuid AND stage = $3`, t.kind, t.id, t.stage)
			}
			if err != nil {
				return internal(req.Action, err)
			}
			if n, _ := r.RowsAffected(); n > 0 {
				res.Affected++
			}
		}
	case FixRescan:
		libs := map[string]bool{}
		for _, t := range ts {
			if t.lib.Valid {
				libs[t.lib.String] = true
			}
		}
		for lib := range libs {
			if _, err := Enqueue(ctx, tx, EnqueueParams{SubjectKind: KindLibrary, SubjectID: lib, Stage: "scan",
				Force: true, Reason: ReasonFor(ctx, "rescan from the failures page", nil)}); err != nil {
				return internal("rescan", err)
			}
			res.Affected++
		}
	case FixRemove:
		ids := subjectIDs(videos())
		res.Skipped = len(ts) - len(ids)
		if len(ids) == 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET state = 'cancelled', finished_at = now()
			WHERE subject_kind = 'video' AND subject_id = ANY($1::uuid[]) AND state IN ('pending', 'paused')`,
			pq.Array(ids)); err != nil {
			return internal("cancel jobs", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET cancel_requested = true
			WHERE subject_kind = 'video' AND subject_id = ANY($1::uuid[])
			  AND state IN ('claimed', 'running', 'resuming')`, pq.Array(ids)); err != nil {
			return internal("cancel jobs", err)
		}
		r, err := tx.ExecContext(ctx, `DELETE FROM videos WHERE id = ANY($1::uuid[])`, pq.Array(ids))
		if err != nil {
			return internal("remove", err)
		}
		n, _ := r.RowsAffected()
		res.Affected = int(n)
	case FixRedownload:
		libs := map[string]bool{}
		for _, t := range videos() {
			var yt sql.NullString
			err := tx.QueryRowContext(ctx, `SELECT external_id FROM media_metadata_enrichment
				WHERE video_id = $1::uuid AND provider = 'youtube' AND NOT is_dismissed LIMIT 1`, t.id).Scan(&yt)
			if errors.Is(err, sql.ErrNoRows) || (err == nil && !yt.Valid) {
				continue
			}
			if err != nil {
				return internal("youtube id", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO item_requests (video_id, action, requested_by)
				VALUES ($1::uuid, 'redownload', $2)`, t.id, by(ctx)); err != nil {
				return internal("redownload", err)
			}
			if t.lib.Valid {
				libs[t.lib.String] = true
			}
			res.Affected++
		}
		res.Skipped = len(ts) - res.Affected
		for lib := range libs {
			if _, err := Enqueue(ctx, tx, EnqueueParams{SubjectKind: KindLibrary, SubjectID: lib, Stage: "repair_broken",
				Force: true, Reason: ReasonFor(ctx, "download again from the failures page", nil)}); err != nil {
				return internal("redownload", err)
			}
		}
	case FixPath:
		vs := videos()
		if len(vs) != 1 {
			return httperror.BadRequest("fix_path needs one video item")
		}
		p := strings.TrimSpace(req.Path)
		if _, err := tx.ExecContext(ctx, `UPDATE videos SET path = $2, filename = $3, missing_confirmed_at = NULL
			WHERE id = $1::uuid`, vs[0].id, p, path.Base(strings.ReplaceAll(p, `\`, "/"))); err != nil {
			var pqErr *pq.Error
			if errors.As(err, &pqErr) && pqErr.Code == "23505" {
				return httperror.Conflict("", "another video of the library has that path")
			}
			return internal("fix path", err)
		}
		n, err := retryTargets(ctx, tx, vs)
		if err != nil {
			return internal("retry", err)
		}
		res.Affected = n
	case FixPathMap:
		// the home's "share -> local mount" override, the one the Machines page edits
		r, err := tx.ExecContext(ctx, `UPDATE instance_identity
			SET path_map = COALESCE(path_map, '{}'::jsonb) || jsonb_build_object($1::text, $2::text),
			    updated_at = now() WHERE id = 1`,
			strings.TrimSpace(req.PathMap.From), strings.TrimSpace(req.PathMap.To))
		if err != nil {
			return internal("path map", err)
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return httperror.Conflict("", "this home has no identity yet: set up the cluster first")
		}
		n, err := retryTargets(ctx, tx, ts)
		if err != nil {
			return internal("retry", err)
		}
		res.Affected = n
	case FixCheckAccess:
		seen := map[string]bool{}
		for _, t := range videos() {
			if seen[t.id] {
				continue
			}
			seen[t.id] = true
			if _, err := Enqueue(ctx, tx, EnqueueParams{SubjectKind: KindVideo, SubjectID: t.id, Stage: "access_check",
				Force: true, Reason: ReasonFor(ctx, "access check from the failures page", nil)}); err != nil {
				return internal("check access", err)
			}
			res.Affected++
		}
		res.Skipped = len(ts) - len(videos())
	}
	return nil
}

// retryTargets: a failed item gets a new forced job (attempts start at
// 0); a parked or backing-off one is released now.
func retryTargets(ctx context.Context, tx *sql.Tx, ts []fixTarget) (int, error) {
	n := 0
	var pending []int64
	for _, t := range ts {
		if t.state == "pending" {
			pending = append(pending, t.jobID)
			continue
		}
		res, err := Enqueue(ctx, tx, EnqueueParams{SubjectKind: t.kind, SubjectID: t.id, Stage: t.stage, Force: true,
			Reason: ReasonFor(ctx, "retry from the failures page", map[string]any{"of_job": t.jobID})})
		if err != nil {
			return n, err
		}
		if res.Accepted() {
			n++
		}
	}
	if len(pending) > 0 {
		r, err := tx.ExecContext(ctx, `UPDATE processing_jobs SET parked_on = NULL, not_before = now()
			WHERE id = ANY($1::bigint[]) AND state = 'pending'`, pq.Array(pending))
		if err != nil {
			return n, err
		}
		m, _ := r.RowsAffected()
		n += int(m)
	}
	return n, nil
}

func subjectIDs(ts []fixTarget) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range ts {
		if !seen[t.id] {
			seen[t.id] = true
			out = append(out, t.id)
		}
	}
	return out
}

// auditFix writes the action to audit_log: who, what, on which items.
func auditFix(ctx context.Context, tx *sql.Tx, req FixRequest, res FixResult, ts []fixTarget) error {
	ids := make([]int64, 0, 200)
	for _, t := range ts {
		if len(ids) == 200 {
			break
		}
		ids = append(ids, t.jobID)
	}
	payload := map[string]any{"matched": res.Matched, "affected": res.Affected, "skipped": res.Skipped,
		"job_ids": ids}
	if req.Filter != nil {
		payload["filter"] = req.Filter
	}
	if req.Path != "" {
		payload["path"] = req.Path
	}
	if req.PathMap != nil {
		payload["path_map"] = req.PathMap
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var actor any
	if p := principal.FromContext(ctx); p != nil {
		if _, err := uuid.Parse(p.UserID); err == nil {
			actor = p.UserID
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_log (category, action, actor_user_id, target_kind,
		actor_source, payload) VALUES ('admin', $1, $2, 'processing_jobs', 'admin', $3)`,
		"queue.failures."+req.Action, actor, string(b))
	return err
}
