package queues

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

var levelOrder = []string{"debug", "info", "warn", "error"}

// levelsFrom returns minLevel and everything more severe; ok is false
// for an unknown level.
func levelsFrom(minLevel string) ([]string, bool) {
	for i, l := range levelOrder {
		if l == minLevel {
			return levelOrder[i:], true
		}
	}
	return nil, false
}

type logFilter struct {
	Stage     string
	LibraryID string
	MinLevel  string
	JobID     int64
	Before    int64 // keyset: only rows with id < Before
	Limit     int   // rows to fetch (callers add 1 to detect "more")
	Asc       bool
	// Scope limits the rows to these libraries (libraryScope: nil = all).
	Scope any
}

// logLines reads job_events with subject labels.
func (h *Handler) logLines(ctx context.Context, f logFilter) ([]LogLine, error) {
	joins, expr := labelSQL("e.subject_kind", "e.subject_id")
	q := `SELECT e.id, e.at, e.job_id, e.stage, e.subject_kind, e.subject_id::text, ` + expr + `,
	       e.level, e.kind, e.message, e.data
	  FROM job_events e` + joins + ` WHERE TRUE`
	args := []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		q += ` AND ` + cond + strconv.Itoa(len(args))
	}
	if f.Stage != "" {
		add(`e.stage = $`, f.Stage)
	}
	if f.LibraryID != "" {
		add(`e.library_id = $`, f.LibraryID)
	}
	if f.JobID > 0 {
		add(`e.job_id = $`, f.JobID)
	}
	if f.Scope != nil {
		args = append(args, f.Scope)
		q += ` AND e.library_id = ANY($` + strconv.Itoa(len(args)) + `::uuid[])`
	}
	if levels, ok := levelsFrom(f.MinLevel); ok && f.MinLevel != "debug" {
		args = append(args, pq.Array(levels))
		q += ` AND e.level = ANY($` + strconv.Itoa(len(args)) + `::text[])`
	}
	if f.Before > 0 {
		add(`e.id < $`, f.Before)
	}
	if f.Asc {
		q += ` ORDER BY e.id`
	} else {
		q += ` ORDER BY e.id DESC`
	}
	if f.Limit > 0 {
		args = append(args, f.Limit)
		q += ` LIMIT $` + strconv.Itoa(len(args))
	}
	rows, err := h.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogLine{}
	for rows.Next() {
		var l LogLine
		var data []byte
		if err := rows.Scan(&l.ID, &l.At, &l.JobID, &l.Stage, &l.SubjectKind, &l.SubjectID,
			&l.SubjectLabel, &l.Level, &l.Kind, &l.Message, &data); err != nil {
			return nil, err
		}
		if len(data) > 0 {
			l.Data = data
		} else {
			l.Data = []byte("null")
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Activity implements GET /api/activity.
func (h *Handler) Activity(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	q := r.URL.Query()
	limit, e := common.QueryInt(r, "limit", 100)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	level := q.Get("level")
	if level == "" {
		level = "info"
	}
	if _, ok := levelsFrom(level); !ok {
		httperror.Write(w, r, httperror.InvalidQuery("level must be one of debug, info, warn, error"))
		return
	}
	lib := q.Get("library_id")
	if lib != "" && !isUUID(lib) {
		httperror.Write(w, r, httperror.InvalidQuery("library_id must be a uuid"))
		return
	}
	p := principal.FromContext(r.Context())
	if lib != "" && !canReadLibrary(p, lib) {
		httperror.Write(w, r, httperror.NotFound("library "+lib))
		return
	}
	before, ok := parseCursor(q.Get("cursor"))
	if !ok {
		httperror.Write(w, r, httperror.InvalidQuery("cursor must be an event id"))
		return
	}
	items, err := h.logLines(r.Context(), logFilter{
		Stage: q.Get("stage"), LibraryID: lib, MinLevel: level, Before: before, Limit: limit + 1,
		Scope: libraryScope(p),
	})
	if err != nil {
		writeErr(w, r, "activity", err)
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		c := strconv.FormatInt(items[len(items)-1].ID, 10)
		next = &c
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items, "next": next})
}

// jobEventsCap bounds one job's log (a transcribe job can be chatty).
const jobEventsCap = 1000

// JobEvents implements GET /api/jobs/{id}/events (oldest first).
func (h *Handler) JobEvents(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httperror.Write(w, r, httperror.InvalidQuery("job id must be a positive integer"))
		return
	}
	// The job's library decides, as for GET /api/jobs/{id}: unknown,
	// another library's or a library-less job (admin only) is 404.
	var lib sql.NullString
	if err := h.DB.QueryRowContext(r.Context(), `SELECT library_id::text FROM processing_jobs WHERE id = $1`, id).Scan(&lib); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("job "+strconv.FormatInt(id, 10)))
			return
		}
		writeErr(w, r, "job events", err)
		return
	}
	var libPtr *string
	if lib.Valid {
		libPtr = &lib.String
	}
	if !canSeeLibraryOf(principal.FromContext(r.Context()), libPtr) {
		httperror.Write(w, r, httperror.NotFound("job "+strconv.FormatInt(id, 10)))
		return
	}
	items, err := h.logLines(r.Context(), logFilter{JobID: id, MinLevel: "debug", Limit: jobEventsCap, Asc: true})
	if err != nil {
		writeErr(w, r, "job events", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}
