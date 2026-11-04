package queues

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
)

const (
	librariesSQL = `SELECT id::text, name FROM libraries ORDER BY name`

	libraryVideosSQL = `SELECT library_id::text, count(*) FROM videos GROUP BY library_id`

	librarySeasonsSQL = `
SELECT library_id::text, count(*) FROM series_seasons
 WHERE retired_at IS NULL AND library_id IS NOT NULL GROUP BY library_id`

	// libraryDoneSQL: the subjects done per library and stage, from the
	// summary the processing_jobs triggers keep (slot 0175): one row per
	// (library, stage, subject), however many times the subject ran, so the
	// count no longer reads the jobs' history (count(DISTINCT subject_id)
	// over every done row was a full scan of 3M+ rows).
	libraryDoneSQL = `
SELECT library_id::text, stage, count(*), max(last_at)
  FROM processing_done_subjects
 GROUP BY library_id, stage`

	// libraryLiveSQL: the live rows only (processing_jobs_live_idx).
	libraryLiveSQL = `
SELECT library_id::text, stage,
       count(*) FILTER (WHERE state IN ('claimed','running','resuming')),
       count(*) FILTER (WHERE state IN ('pending','paused')),
       max(created_at)
  FROM processing_jobs
 WHERE state IN ('pending','claimed','running','resuming','paused') AND library_id IS NOT NULL
 GROUP BY library_id, stage`

	// libraryEndedSQL: when a row last failed or was cancelled (the done rows'
	// times are in the summary, the live rows' in libraryLiveSQL). The
	// retention keeps these rows 90 days, so the read follows recent failures.
	libraryEndedSQL = `
SELECT library_id::text, stage, max(COALESCE(finished_at, created_at))
  FROM processing_jobs
 WHERE state IN ('failed','cancelled') AND library_id IS NOT NULL
 GROUP BY library_id, stage`

	libraryFailedSQL = `
WITH f AS (
    SELECT DISTINCT subject_kind, subject_id, stage
      FROM processing_jobs WHERE state = 'failed' AND library_id IS NOT NULL
), latest AS (
    SELECT l.library_id, l.stage, l.state
      FROM f
      CROSS JOIN LATERAL (
           SELECT j.library_id, j.stage, j.state FROM processing_jobs j
            WHERE j.subject_kind = f.subject_kind AND j.subject_id = f.subject_id AND j.stage = f.stage
            ORDER BY j.id DESC LIMIT 1) l
)
SELECT library_id::text, stage, count(*) FROM latest WHERE state = 'failed' GROUP BY library_id, stage`
)

// stripKinds are the subject kinds a library strip shows, in the order
// used to pick the denominator of a stage.
var stripKinds = []string{KindVideo, KindSeason, KindLibrary}

// stripTotalKind picks which population a stage's progress is measured
// against: its videos, its seasons, or the library itself.
func stripTotalKind(kinds []string) string {
	for _, want := range stripKinds {
		for _, k := range kinds {
			if k == want {
				return want
			}
		}
	}
	return ""
}

type stageCountKey struct{ lib, stage string }

type stageCountVal struct {
	done, running, pending int
	last                   *time.Time
}

// Libraries implements GET /api/queues/libraries, from the shared read
// cache (readcache.go): fresh for libraryCacheTTL, then served stale while
// one rebuild runs.
func (h *Handler) Libraries(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	items, err := h.reads().libraries.Get(r.Context(), h.now(), h.buildLibraryRows)
	if err != nil {
		writeErr(w, r, "libraries", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": visibleLibraryRows(principal.FromContext(r.Context()), items)})
}

// visibleLibraryRows keeps the libraries p may read (the cache is global).
func visibleLibraryRows(p *principal.Principal, items []LibraryQueueRow) []LibraryQueueRow {
	if fullView(p) {
		return items
	}
	out := []LibraryQueueRow{}
	for _, it := range items {
		if canReadLibrary(p, it.LibraryID) {
			out = append(out, it)
		}
	}
	return out
}

func (h *Handler) countsByLibrary(ctx context.Context, q string) (map[string]int, error) {
	rows, err := h.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id sql.NullString
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		if id.Valid {
			out[id.String] = n
		}
	}
	return out, rows.Err()
}

// scanStageRows runs q, whose rows are library_id, stage and then dest's
// columns, and calls each with every row's key once dest holds its values.
func (h *Handler) scanStageRows(ctx context.Context, q string, dest []any, each func(stageCountKey)) error {
	rows, err := h.DB.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k stageCountKey
		if err := rows.Scan(append([]any{&k.lib, &k.stage}, dest...)...); err != nil {
			return err
		}
		each(k)
	}
	return rows.Err()
}

func (h *Handler) buildLibraryRows(ctx context.Context) ([]LibraryQueueRow, error) {
	stages, err := h.loadStages(ctx)
	if err != nil {
		return nil, err
	}
	videos, err := h.countsByLibrary(ctx, libraryVideosSQL)
	if err != nil {
		return nil, err
	}
	seasons, err := h.countsByLibrary(ctx, librarySeasonsSQL)
	if err != nil {
		return nil, err
	}

	counts := map[stageCountKey]*stageCountVal{}
	val := func(k stageCountKey) *stageCountVal {
		v := counts[k]
		if v == nil {
			v = &stageCountVal{}
			counts[k] = v
		}
		return v
	}
	later := func(v *stageCountVal, t sql.NullTime) {
		if t.Valid && (v.last == nil || t.Time.After(*v.last)) {
			at := t.Time
			v.last = &at
		}
	}
	var n, running, pending int
	var last sql.NullTime
	if err := h.scanStageRows(ctx, libraryDoneSQL, []any{&n, &last}, func(k stageCountKey) {
		v := val(k)
		v.done = n
		later(v, last)
	}); err != nil {
		return nil, err
	}
	if err := h.scanStageRows(ctx, libraryLiveSQL, []any{&running, &pending, &last}, func(k stageCountKey) {
		v := val(k)
		v.running, v.pending = running, pending
		later(v, last)
	}); err != nil {
		return nil, err
	}
	if err := h.scanStageRows(ctx, libraryEndedSQL, []any{&last}, func(k stageCountKey) {
		later(val(k), last)
	}); err != nil {
		return nil, err
	}

	failed := map[stageCountKey]int{}
	frows, err := h.DB.QueryContext(ctx, libraryFailedSQL)
	if err != nil {
		return nil, err
	}
	for frows.Next() {
		var k stageCountKey
		var n int
		if err := frows.Scan(&k.lib, &k.stage, &n); err != nil {
			frows.Close()
			return nil, err
		}
		failed[k] = n
	}
	if err := frows.Err(); err != nil {
		frows.Close()
		return nil, err
	}
	frows.Close()

	lrows, err := h.DB.QueryContext(ctx, librariesSQL)
	if err != nil {
		return nil, err
	}
	defer lrows.Close()
	out := []LibraryQueueRow{}
	for lrows.Next() {
		var row LibraryQueueRow
		if err := lrows.Scan(&row.LibraryID, &row.Name); err != nil {
			return nil, err
		}
		row.Videos, row.Seasons = videos[row.LibraryID], seasons[row.LibraryID]
		row.Strip = []StripCell{}
		for _, st := range stages {
			if st.Hidden {
				continue
			}
			tk := stripTotalKind(st.SubjectKinds)
			if tk == "" {
				continue
			}
			cell := StripCell{Stage: st.Stage}
			switch tk {
			case KindVideo:
				cell.Total = row.Videos
			case KindSeason:
				cell.Total = row.Seasons
			default:
				cell.Total = 1
			}
			key := stageCountKey{row.LibraryID, st.Stage}
			if v := counts[key]; v != nil {
				cell.Done, cell.Running, cell.Pending = v.done, v.running, v.pending
				if v.last != nil && (row.LastActivityAt == nil || v.last.After(*row.LastActivityAt)) {
					t := *v.last
					row.LastActivityAt = &t
				}
			}
			cell.Failed = failed[key]
			if cell.Done > cell.Total {
				cell.Done = cell.Total
			}
			row.Strip = append(row.Strip, cell)
		}
		out = append(out, row)
	}
	return out, lrows.Err()
}
