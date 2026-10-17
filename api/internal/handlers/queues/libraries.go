package queues

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
)

// libraryCacheTTL is how long GET /api/queues/libraries may be served
// from memory (plan-06-11 §8.1: "cached 5 s").
const libraryCacheTTL = 5 * time.Second

type libraryCache struct {
	at    time.Time
	items []LibraryQueueRow
}

const (
	librariesSQL = `SELECT id::text, name FROM libraries ORDER BY name`

	libraryVideosSQL = `SELECT library_id::text, count(*) FROM videos GROUP BY library_id`

	librarySeasonsSQL = `
SELECT library_id::text, count(*) FROM series_seasons
 WHERE retired_at IS NULL AND library_id IS NOT NULL GROUP BY library_id`

	libraryStageCountsSQL = `
SELECT library_id::text, stage,
       count(DISTINCT subject_id) FILTER (WHERE state = 'done'),
       count(*) FILTER (WHERE state IN ('claimed','running','resuming')),
       count(*) FILTER (WHERE state IN ('pending','paused')),
       max(COALESCE(finished_at, created_at))
  FROM processing_jobs
 WHERE library_id IS NOT NULL
 GROUP BY library_id, stage`

	libraryFailedSQL = `
WITH f AS (
    SELECT DISTINCT subject_kind, subject_id, stage
      FROM processing_jobs WHERE state = 'failed' AND library_id IS NOT NULL
), latest AS (
    SELECT DISTINCT ON (j.subject_kind, j.subject_id, j.stage)
           j.library_id, j.stage, j.state
      FROM processing_jobs j
      JOIN f ON f.subject_kind = j.subject_kind AND f.subject_id = j.subject_id AND f.stage = j.stage
     ORDER BY j.subject_kind, j.subject_id, j.stage, j.id DESC
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

// Libraries implements GET /api/queues/libraries.
func (h *Handler) Libraries(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	now := h.now()
	h.libMu.Lock()
	if h.libCache.items != nil && now.Sub(h.libCache.at) < libraryCacheTTL {
		items := h.libCache.items
		h.libMu.Unlock()
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
		return
	}
	h.libMu.Unlock()

	items, err := h.buildLibraryRows(r.Context())
	if err != nil {
		writeErr(w, r, "libraries", err)
		return
	}
	h.libMu.Lock()
	h.libCache = libraryCache{at: now, items: items}
	h.libMu.Unlock()
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
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
	crows, err := h.DB.QueryContext(ctx, libraryStageCountsSQL)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var k stageCountKey
		var v stageCountVal
		var last sql.NullTime
		if err := crows.Scan(&k.lib, &k.stage, &v.done, &v.running, &v.pending, &last); err != nil {
			crows.Close()
			return nil, err
		}
		v.last = nullTimePtr(last)
		counts[k] = &v
	}
	if err := crows.Err(); err != nil {
		crows.Close()
		return nil, err
	}
	crows.Close()

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
