package queues

// The LibrariesTable's Epic 47 columns:
//
//	GET /api/libraries/{id}/offline-summary  one library
//	GET /api/queues/offline-summary          every library, plus the open
//	                                         cross-library duplicate groups
//
// YouTube (sources on; wanted, downloaded, failed; back catalogue waiting;
// last refresh), Not MP4 (live files that are not .mp4/.m4v), Duplicates
// (open groups) and the trash (entries, bytes, next purge).

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// SourceProblem is a source that is on and did not list cleanly last time.
type SourceProblem struct {
	SourceID string  `json:"source_id"`
	Kind     string  `json:"kind"`
	URL      string  `json:"url"`
	Status   string  `json:"status"`
	Error    *string `json:"error"`
}

// YouTubeSummary is a library's YouTube column.
type YouTubeSummary struct {
	Sources     int             `json:"sources"`
	SourcesOn   int             `json:"sources_on"`
	Wanted      int             `json:"wanted"`
	Backfill    int             `json:"backfill"`
	Downloading int             `json:"downloading"`
	Downloaded  int             `json:"downloaded"`
	Failed      int             `json:"failed"`
	Skipped     int             `json:"skipped"`
	Gone        int             `json:"gone"`
	LastRefresh *time.Time      `json:"last_refresh"`
	LastFetched *time.Time      `json:"last_fetched"`
	Problems    []SourceProblem `json:"problems"`
}

// TrashSummary is what a library's trash holds until its purge.
type TrashSummary struct {
	Entries     int        `json:"entries"`
	Bytes       int64      `json:"bytes"`
	NextPurgeAt *time.Time `json:"next_purge_at"`
}

// OfflineSummary is one library's Epic 47 columns.
type OfflineSummary struct {
	LibraryID  string         `json:"library_id"`
	Name       string         `json:"name"`
	YouTube    YouTubeSummary `json:"youtube"`
	NotMP4     int            `json:"not_mp4"`
	Converting int            `json:"converting"`
	Duplicates int            `json:"duplicates"`
	Trash      TrashSummary   `json:"trash"`
}

const (
	summaryLibrariesSQL = `SELECT id::text, name FROM libraries
 WHERE deleted_at IS NULL AND ($1::uuid IS NULL OR id = $1) ORDER BY name, id`

	summarySourcesSQL = `
SELECT library_id::text, count(*), count(*) FILTER (WHERE mode <> 'off'),
       max(last_checked_at), max(last_fetched_at)
  FROM youtube_sources WHERE ($1::uuid IS NULL OR library_id = $1) GROUP BY library_id`

	summaryProblemsSQL = `
SELECT library_id::text, id::text, kind, url, status, error FROM youtube_sources
 WHERE mode <> 'off' AND status IS NOT NULL AND status <> 'ok' AND ($1::uuid IS NULL OR library_id = $1)
 ORDER BY library_id, kind, external_id`

	summaryDownloadsSQL = `
SELECT library_id::text,
       count(*) FILTER (WHERE state = 'wanted' AND NOT backfill),
       count(*) FILTER (WHERE state = 'wanted' AND backfill),
       count(*) FILTER (WHERE state = 'downloading'),
       count(*) FILTER (WHERE state = 'done'),
       count(*) FILTER (WHERE state = 'failed'),
       count(*) FILTER (WHERE state = 'skipped'),
       count(*) FILTER (WHERE state = 'gone')
  FROM youtube_downloads WHERE ($1::uuid IS NULL OR library_id = $1) GROUP BY library_id`

	// the convert stage's own test (library_mgmt/convert.py MP4_EXTS)
	summaryNotMP4SQL = `
SELECT library_id::text, count(*) FROM videos
 WHERE deleted_at IS NULL AND superseded_by IS NULL AND lower(path) !~ '\.(mp4|m4v)$'
   AND ($1::uuid IS NULL OR library_id = $1)
 GROUP BY library_id`

	summaryConvertingSQL = `
SELECT library_id::text, count(*) FROM processing_jobs
 WHERE stage = 'convert_mp4' AND subject_kind = 'video'
   AND state IN ('pending','claimed','running','resuming','paused')
   AND ($1::uuid IS NULL OR library_id = $1)
 GROUP BY library_id`

	summaryDuplicatesSQL = `
SELECT library_id::text, count(*) FROM duplicate_groups
 WHERE status = 'open' AND library_id IS NOT NULL AND ($1::uuid IS NULL OR library_id = $1)
 GROUP BY library_id`

	crossLibraryDuplicatesSQL = `SELECT count(*) FROM duplicate_groups WHERE status = 'open' AND library_id IS NULL`

	// what is in the trash: a done row that moved a file there and was not purged
	summaryTrashSQL = `
SELECT library_id::text, count(*), COALESCE(sum(old_size), 0)::bigint, min(purge_after)
  FROM file_actions
 WHERE state = 'done' AND trash_path IS NOT NULL AND purged_at IS NULL
   AND ($1::uuid IS NULL OR library_id = $1)
 GROUP BY library_id`
)

// buildOfflineSummaries reads the columns of one library (lib) or every one (nil).
func (h *Handler) buildOfflineSummaries(ctx context.Context, lib any) ([]OfflineSummary, error) {
	out := []OfflineSummary{}
	idx := map[string]*OfflineSummary{}
	rows, err := h.DB.QueryContext(ctx, summaryLibrariesSQL, lib)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s OfflineSummary
		if err := rows.Scan(&s.LibraryID, &s.Name); err != nil {
			rows.Close()
			return nil, err
		}
		s.YouTube.Problems = []SourceProblem{}
		out = append(out, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		idx[out[i].LibraryID] = &out[i]
	}
	each := func(q string, scan func(s *OfflineSummary, rs *sql.Rows) error, dest ...any) error {
		rs, err := h.DB.QueryContext(ctx, q, lib)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var id string
			if err := rs.Scan(append([]any{&id}, dest...)...); err != nil {
				return err
			}
			if s := idx[id]; s != nil {
				if err := scan(s, rs); err != nil {
					return err
				}
			}
		}
		return rs.Err()
	}
	var n1, n2, n3, n4, n5, n6, n7 int
	var t1, t2 sql.NullTime
	if err := each(summarySourcesSQL, func(s *OfflineSummary, _ *sql.Rows) error {
		s.YouTube.Sources, s.YouTube.SourcesOn = n1, n2
		s.YouTube.LastRefresh, s.YouTube.LastFetched = nullTimePtr(t1), nullTimePtr(t2)
		return nil
	}, &n1, &n2, &t1, &t2); err != nil {
		return nil, err
	}
	var sid, kind, url, status string
	var perr sql.NullString
	if err := each(summaryProblemsSQL, func(s *OfflineSummary, _ *sql.Rows) error {
		s.YouTube.Problems = append(s.YouTube.Problems, SourceProblem{SourceID: sid, Kind: kind, URL: url,
			Status: status, Error: nullStrPtr(perr)})
		return nil
	}, &sid, &kind, &url, &status, &perr); err != nil {
		return nil, err
	}
	if err := each(summaryDownloadsSQL, func(s *OfflineSummary, _ *sql.Rows) error {
		y := &s.YouTube
		y.Wanted, y.Backfill, y.Downloading, y.Downloaded, y.Failed, y.Skipped, y.Gone = n1, n2, n3, n4, n5, n6, n7
		return nil
	}, &n1, &n2, &n3, &n4, &n5, &n6, &n7); err != nil {
		return nil, err
	}
	for _, c := range []struct {
		q   string
		set func(s *OfflineSummary, n int)
	}{
		{summaryNotMP4SQL, func(s *OfflineSummary, n int) { s.NotMP4 = n }},
		{summaryConvertingSQL, func(s *OfflineSummary, n int) { s.Converting = n }},
		{summaryDuplicatesSQL, func(s *OfflineSummary, n int) { s.Duplicates = n }},
	} {
		if err := each(c.q, func(s *OfflineSummary, _ *sql.Rows) error { c.set(s, n1); return nil }, &n1); err != nil {
			return nil, err
		}
	}
	var bytes int64
	if err := each(summaryTrashSQL, func(s *OfflineSummary, _ *sql.Rows) error {
		s.Trash = TrashSummary{Entries: n1, Bytes: bytes, NextPurgeAt: nullTimePtr(t1)}
		return nil
	}, &n1, &bytes, &t1); err != nil {
		return nil, err
	}
	return out, nil
}

// OfflineSummary implements GET /api/libraries/{id}/offline-summary.
func (h *Handler) OfflineSummary(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryParam(w, r, false)
	if !ok {
		return
	}
	items, err := h.buildOfflineSummaries(r.Context(), id)
	if err != nil {
		writeErr(w, r, "offline summary", err)
		return
	}
	if len(items) == 0 {
		httperror.Write(w, r, httperror.NotFound("library not found"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, items[0])
}

// OfflineSummaries implements GET /api/queues/offline-summary.
func (h *Handler) OfflineSummaries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	items, err := h.buildOfflineSummaries(ctx, nil)
	if err != nil {
		writeErr(w, r, "offline summary", err)
		return
	}
	var cross int
	if err := h.DB.QueryRowContext(ctx, crossLibraryDuplicatesSQL).Scan(&cross); err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeErr(w, r, "offline summary", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items, "cross_library_duplicates": cross})
}
