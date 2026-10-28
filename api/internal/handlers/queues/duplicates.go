package queues

// Duplicate groups (Epic 47, stories 47.6-47.7) for /queue/duplicates:
//
//	GET  /api/queues/duplicates                 filters status (default open), tier,
//	                                            library_id ("cross" = across libraries);
//	                                            keyset paging (cursor, limit)
//	POST /api/queues/duplicates/{gid}/apply     {keeper_video_id?}: dedup_apply, approved
//	POST /api/queues/duplicates/{gid}/dismiss   the scan never makes this group again
//
// Apply only queues the merge (dedup_apply for the group, payload {apply,
// group_id, keeper_video_id?, decided_by}); a same_video or cross-library group
// waits for this approval, an exact one in a library applies by itself.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// DuplicateMember is one file of a group: a video's own path, or one of its copies.
type DuplicateMember struct {
	VideoID     string          `json:"video_id"`
	Path        string          `json:"path"`
	IsCopy      bool            `json:"is_copy"`
	Score       json.RawMessage `json:"score"`
	Title       *string         `json:"title"`
	LibraryID   string          `json:"library_id"`
	LibraryName string          `json:"library_name"`
	SizeBytes   *int64          `json:"size_bytes"`
	DurationSec *float64        `json:"duration_sec"`
	Width       *int            `json:"width"`
	Height      *int            `json:"height"`
	VideoCodec  *string         `json:"video_codec"`
	Container   *string         `json:"container"`
	BitrateKbps *int            `json:"bitrate_kbps"`
	// watch data: who played it, who finished it, when last
	Viewers     int        `json:"viewers"`
	Completed   int        `json:"completed"`
	LastWatched *time.Time `json:"last_watched"`
}

// DuplicateGroup is one group with its members and its latest merge job.
type DuplicateGroup struct {
	ID            string            `json:"id"`
	LibraryID     *string           `json:"library_id"`
	LibraryName   *string           `json:"library_name"`
	Tier          string            `json:"tier"`
	Status        string            `json:"status"`
	KeeperVideoID *string           `json:"keeper_video_id"`
	Reason        *string           `json:"reason"`
	CreatedAt     time.Time         `json:"created_at"`
	DecidedBy     *string           `json:"decided_by"`
	DecidedAt     *time.Time        `json:"decided_at"`
	Job           *JobRef           `json:"job"`
	Members       []DuplicateMember `json:"members"`
}

var (
	dupStatuses = map[string]bool{"open": true, "applied": true, "dismissed": true, "failed": true}
	dupTiers    = map[string]bool{"exact": true, "same_video": true}
)

const dupGroupCols = `g.id::text, g.library_id::text, l.name, g.tier, g.status, g.keeper_video_id::text, g.reason,
       g.created_at, g.decided_by, g.decided_at
  FROM duplicate_groups g LEFT JOIN libraries l ON l.id = g.library_id`

func scanDupGroup(rs rowScanner) (DuplicateGroup, error) {
	var g DuplicateGroup
	var lib, libName, keeper, reason, by sql.NullString
	var at sql.NullTime
	if err := rs.Scan(&g.ID, &lib, &libName, &g.Tier, &g.Status, &keeper, &reason, &g.CreatedAt, &by, &at); err != nil {
		return g, err
	}
	g.LibraryID, g.LibraryName, g.KeeperVideoID = nullStrPtr(lib), nullStrPtr(libName), nullStrPtr(keeper)
	g.Reason, g.DecidedBy, g.DecidedAt = nullStrPtr(reason), nullStrPtr(by), nullTimePtr(at)
	g.Members = []DuplicateMember{}
	return g, nil
}

const dupMembersSQL = `
SELECT m.group_id::text, m.video_id::text, m.path, m.is_copy, m.score, COALESCE(v.title, v.filename),
       v.library_id::text, l.name, v.size_bytes, v.duration_sec, mi.width, mi.height, mi.video_codec,
       mi.container, mi.bitrate_kbps,
       (SELECT count(*) FROM playback_state ps WHERE ps.video_id = m.video_id),
       (SELECT count(*) FROM playback_state ps WHERE ps.video_id = m.video_id AND ps.completed),
       (SELECT max(ps.updated_at) FROM playback_state ps WHERE ps.video_id = m.video_id)
  FROM duplicate_members m
  JOIN videos v ON v.id = m.video_id
  JOIN libraries l ON l.id = v.library_id
  LEFT JOIN media_info mi ON mi.video_id = m.video_id
 WHERE m.group_id = ANY($1::uuid[])
 ORDER BY m.group_id, m.is_copy, m.path`

// Duplicates implements GET /api/queues/duplicates.
func (h *Handler) Duplicates(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status := q.Get("status")
	if status == "" {
		status = "open"
	}
	if status != "all" && !dupStatuses[status] {
		httperror.Write(w, r, httperror.BadRequest("status must be open, applied, dismissed, failed or all"))
		return
	}
	tier := q.Get("tier")
	if tier != "" && !dupTiers[tier] {
		httperror.Write(w, r, httperror.BadRequest("tier must be exact or same_video"))
		return
	}
	lib := q.Get("library_id")
	cross := lib == "cross"
	if lib != "" && !cross && !isUUID(lib) {
		httperror.Write(w, r, httperror.BadRequest(`library_id must be a uuid or "cross"`))
		return
	}
	if cross {
		lib = ""
	}
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
	rows, err := h.DB.QueryContext(ctx, `SELECT `+dupGroupCols+`
 WHERE ($1 = 'all' OR g.status = $1) AND ($2 = '' OR g.tier = $2)
   AND ($3::uuid IS NULL OR g.library_id = $3) AND (NOT $4 OR g.library_id IS NULL)
   AND ($5::timestamptz IS NULL OR (g.created_at, g.id::text) < ($5, $6))
 ORDER BY g.created_at DESC, g.id::text DESC LIMIT $7`,
		status, tier, nullIfEmpty(lib), cross, curAt, curID, limit+1)
	if err != nil {
		writeErr(w, r, "duplicates", err)
		return
	}
	groups := []DuplicateGroup{}
	for rows.Next() {
		g, err := scanDupGroup(rows)
		if err != nil {
			rows.Close()
			writeErr(w, r, "duplicates", err)
			return
		}
		groups = append(groups, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeErr(w, r, "duplicates", err)
		return
	}
	var next *string
	if len(groups) > limit {
		groups = groups[:limit]
		last := groups[len(groups)-1]
		c := makeKeyset(last.CreatedAt, last.ID)
		next = &c
	}
	if err := h.fillGroups(ctx, groups); err != nil {
		writeErr(w, r, "duplicate members", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": groups, "next": next})
}

// fillGroups adds each group's members and its latest dedup_apply job.
func (h *Handler) fillGroups(ctx context.Context, groups []DuplicateGroup) error {
	if len(groups) == 0 {
		return nil
	}
	ids := make([]string, len(groups))
	idx := map[string]*DuplicateGroup{}
	for i := range groups {
		ids[i] = groups[i].ID
		idx[groups[i].ID] = &groups[i]
	}
	rows, err := h.DB.QueryContext(ctx, dupMembersSQL, pq.Array(ids))
	if err != nil {
		return err
	}
	for rows.Next() {
		var gid string
		var m DuplicateMember
		var score []byte
		var title, codec, container sql.NullString
		var size, width, height, bitrate sql.NullInt64
		var dur sql.NullFloat64
		var last sql.NullTime
		if err := rows.Scan(&gid, &m.VideoID, &m.Path, &m.IsCopy, &score, &title, &m.LibraryID, &m.LibraryName,
			&size, &dur, &width, &height, &codec, &container, &bitrate, &m.Viewers, &m.Completed, &last); err != nil {
			rows.Close()
			return err
		}
		m.Title, m.VideoCodec, m.Container = nullStrPtr(title), nullStrPtr(codec), nullStrPtr(container)
		if size.Valid {
			m.SizeBytes = &size.Int64
		}
		if dur.Valid {
			m.DurationSec = &dur.Float64
		}
		m.Width, m.Height, m.BitrateKbps = nullIntPtr(width), nullIntPtr(height), nullIntPtr(bitrate)
		m.LastWatched = nullTimePtr(last)
		if len(score) > 0 {
			m.Score = json.RawMessage(score)
		} else {
			m.Score = json.RawMessage("null")
		}
		if g := idx[gid]; g != nil {
			g.Members = append(g.Members, m)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	jrows, err := h.DB.QueryContext(ctx, jobRefQuery("")+`
 WHERE j.stage = 'dedup_apply' AND j.subject_kind = 'dup_group' AND j.subject_id = ANY($1::uuid[])
   AND NOT EXISTS (SELECT 1 FROM processing_jobs k WHERE k.subject_kind = j.subject_kind
        AND k.subject_id = j.subject_id AND k.stage = j.stage AND k.id > j.id)`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer jrows.Close()
	for jrows.Next() {
		j, err := scanJobRef(jrows)
		if err != nil {
			return err
		}
		if g := idx[j.SubjectID]; g != nil {
			jc := j
			g.Job = &jc
		}
	}
	return jrows.Err()
}

func nullIntPtr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}

// DuplicateDecision is the body of apply (and the optional one of dismiss).
type DuplicateDecision struct {
	KeeperVideoID string `json:"keeper_video_id"`
}

// mergeApplyPayloadSQL writes the approval onto the dedup_apply job the enqueue
// landed on (the enqueue keeps an existing row's payload); the last decision wins.
const mergeApplyPayloadSQL = `UPDATE processing_jobs
   SET payload = (COALESCE(payload, '{}'::jsonb) - 'keeper_video_id' - 'dry_run') || $2::jsonb WHERE id = $1`

func (h *Handler) lockGroup(ctx context.Context, tx *sql.Tx, gid string) (DuplicateGroup, error) {
	return scanDupGroup(tx.QueryRowContext(ctx, `SELECT `+dupGroupCols+` WHERE g.id = $1 FOR UPDATE OF g`, gid))
}

func groupParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	gid := chi.URLParam(r, "gid")
	if !isUUID(gid) {
		httperror.Write(w, r, httperror.BadRequest("group id must be a uuid"))
		return "", false
	}
	return gid, true
}

func decidedBy(ctx context.Context) string {
	if p := principal.FromContext(ctx); p != nil && p.UserID != "" {
		return "user:" + p.UserID
	}
	return "system"
}

// ApplyDuplicate implements POST /api/queues/duplicates/{gid}/apply.
func (h *Handler) ApplyDuplicate(w http.ResponseWriter, r *http.Request) {
	gid, ok := groupParam(w, r)
	if !ok {
		return
	}
	var req DuplicateDecision
	if e := readOptionalJSON(r, &req); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.KeeperVideoID != "" && !isUUID(req.KeeperVideoID) {
		httperror.Write(w, r, httperror.BadRequest("keeper_video_id must be a uuid"))
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		writeErr(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	g, err := h.lockGroup(ctx, tx, gid)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("duplicate group not found"))
		return
	}
	if err != nil {
		writeErr(w, r, "duplicate group", err)
		return
	}
	if g.Status != "open" && g.Status != "failed" {
		httperror.Write(w, r, httperror.Conflict("", "the group is "+g.Status).With("status", g.Status))
		return
	}
	if req.KeeperVideoID != "" {
		var member bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM duplicate_members WHERE group_id = $1 AND video_id = $2)`,
			gid, req.KeeperVideoID).Scan(&member); err != nil {
			writeErr(w, r, "duplicate group", err)
			return
		}
		if !member {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "keeper_video_id",
				Message: "not a member of the group"}}))
			return
		}
		if _, err := tx.ExecContext(ctx, `UPDATE duplicate_groups SET keeper_video_id = $2 WHERE id = $1`,
			gid, req.KeeperVideoID); err != nil {
			writeErr(w, r, "keeper", err)
			return
		}
	}
	payload := map[string]any{"apply": true, "group_id": gid, "decided_by": decidedBy(ctx)}
	if req.KeeperVideoID != "" {
		payload["keeper_video_id"] = req.KeeperVideoID
	}
	res, err := Enqueue(ctx, tx, EnqueueParams{SubjectKind: KindDupGroup, SubjectID: gid, Stage: StageDedupApply,
		Priority: prio(StageDedupApply), Force: true, Payload: payload,
		Reason: ReasonFor(ctx, "apply duplicates", map[string]any{"group_id": gid})})
	if err != nil {
		writeErr(w, r, "enqueue", err)
		return
	}
	if !res.Accepted() {
		httperror.Write(w, r, httperror.Conflict("", "the merge queue does not take it: "+res.Outcome).
			With("outcome", res.Outcome))
		return
	}
	b, _ := json.Marshal(payload)
	if _, err := tx.ExecContext(ctx, mergeApplyPayloadSQL, *res.JobID, string(b)); err != nil {
		writeErr(w, r, "enqueue", err)
		return
	}
	if err := auditAdmin(ctx, tx, "queue.duplicates.apply", "duplicate_group", gid, map[string]any{
		"library_id": g.LibraryID, "tier": g.Tier, "keeper_video_id": nullIfEmpty(req.KeeperVideoID),
		"job_id": res.JobID, "outcome": res.Outcome,
	}); err != nil {
		writeErr(w, r, "audit", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, r, "commit", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"group_id": gid, "job_id": res.JobID, "outcome": res.Outcome})
}

// DismissDuplicate implements POST /api/queues/duplicates/{gid}/dismiss. No job:
// the group's members_key is remembered, so the scan never makes it again.
func (h *Handler) DismissDuplicate(w http.ResponseWriter, r *http.Request) {
	gid, ok := groupParam(w, r)
	if !ok {
		return
	}
	var req struct{}
	if e := readOptionalJSON(r, &req); e != nil {
		httperror.Write(w, r, e)
		return
	}
	ctx := r.Context()
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		writeErr(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	g, err := h.lockGroup(ctx, tx, gid)
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("duplicate group not found"))
		return
	}
	if err != nil {
		writeErr(w, r, "duplicate group", err)
		return
	}
	if g.Status != "open" && g.Status != "failed" {
		httperror.Write(w, r, httperror.Conflict("", "the group is "+g.Status).With("status", g.Status))
		return
	}
	if _, err := tx.ExecContext(ctx, `UPDATE duplicate_groups SET status = 'dismissed', decided_by = $2, decided_at = now()
		WHERE id = $1`, gid, decidedBy(ctx)); err != nil {
		writeErr(w, r, "dismiss", err)
		return
	}
	if err := auditAdmin(ctx, tx, "queue.duplicates.dismiss", "duplicate_group", gid, map[string]any{
		"library_id": g.LibraryID, "tier": g.Tier}); err != nil {
		writeErr(w, r, "audit", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, r, "commit", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"group_id": gid, "status": "dismissed"})
}
