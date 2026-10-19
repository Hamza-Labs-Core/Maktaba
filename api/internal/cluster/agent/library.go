package agent

import (
	"context"
	"database/sql"
	"math"
	"time"
)

// LibraryStats is one library in a library snapshot.
type LibraryStats struct {
	LibraryID         string  `json:"library_id,omitempty"`
	Name              string  `json:"name,omitempty"`
	Videos            int64   `json:"videos"`
	Hours             float64 `json:"hours"`
	Bytes             int64   `json:"bytes"`
	TranscribedVideos int64   `json:"transcribed_videos"`
	TranscribedHours  float64 `json:"transcribed_hours"`
	Series            int64   `json:"series"`
	Seasons           int64   `json:"seasons"`
	Tagged            int64   `json:"tagged"`
	Indexed           int64   `json:"indexed"`
}

// LibrarySnapshot is the `library` message and one line of
// library/<date>/hub.jsonl.
type LibrarySnapshot struct {
	TS        time.Time      `json:"ts"`
	Libraries []LibraryStats `json:"libraries"`
	Totals    LibraryStats   `json:"totals"`
}

// Videos counted: present (not deleted in the UI, not missing on disk,
// not superseded). Transcribed: an active transcript. Tagged: a user tag
// or a topic tag. Indexed: a transcript unit with an embedding.
const libraryVideosSQL = `
WITH v AS (
  SELECT id, library_id, COALESCE(duration_sec, 0)::float8 AS dur, size_bytes
    FROM videos
   WHERE deleted_at IS NULL AND state NOT IN ('missing', 'superseded')
), t AS (SELECT DISTINCT video_id FROM transcripts WHERE is_active),
   tg AS (SELECT video_id FROM video_tags UNION SELECT video_id FROM video_topic_tags),
   ix AS (SELECT DISTINCT video_id FROM transcript_units WHERE embedding_id IS NOT NULL)
SELECT l.id::text, l.name,
       count(v.id),
       COALESCE(sum(v.dur), 0)::float8,
       COALESCE(sum(v.size_bytes), 0)::bigint,
       count(t.video_id),
       COALESCE(sum(v.dur) FILTER (WHERE t.video_id IS NOT NULL), 0)::float8,
       count(tg.video_id),
       count(ix.video_id)
  FROM libraries l
  LEFT JOIN v ON v.library_id = l.id
  LEFT JOIN t ON t.video_id = v.id
  LEFT JOIN tg ON tg.video_id = v.id
  LEFT JOIN ix ON ix.video_id = v.id
 WHERE l.deleted_at IS NULL
 GROUP BY l.id, l.name
 ORDER BY l.name, l.id`

const librarySeriesSQL = `
SELECT library_id::text, count(*) FROM series WHERE library_id IS NOT NULL GROUP BY library_id`

const librarySeasonsSQL = `
SELECT library_id::text, count(*) FROM series_seasons
 WHERE library_id IS NOT NULL AND retired_at IS NULL GROUP BY library_id`

// DBLibrary builds library snapshots from the catalog.
func DBLibrary(db *sql.DB, now func() time.Time) func(ctx context.Context) (*LibrarySnapshot, error) {
	return func(ctx context.Context) (*LibrarySnapshot, error) {
		rows, err := db.QueryContext(ctx, libraryVideosSQL)
		if err != nil {
			return nil, err
		}
		snap := &LibrarySnapshot{TS: now().UTC(), Libraries: []LibraryStats{}}
		idx := map[string]int{}
		for rows.Next() {
			var s LibraryStats
			var dur, tdur float64
			if err := rows.Scan(&s.LibraryID, &s.Name, &s.Videos, &dur, &s.Bytes, &s.TranscribedVideos, &tdur,
				&s.Tagged, &s.Indexed); err != nil {
				_ = rows.Close()
				return nil, err
			}
			s.Hours, s.TranscribedHours = round2(dur/3600), round2(tdur/3600)
			idx[s.LibraryID] = len(snap.Libraries)
			snap.Libraries = append(snap.Libraries, s)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for _, q := range []struct {
			sql string
			set func(*LibraryStats, int64)
		}{
			{librarySeriesSQL, func(s *LibraryStats, n int64) { s.Series = n }},
			{librarySeasonsSQL, func(s *LibraryStats, n int64) { s.Seasons = n }},
		} {
			if err := scanCounts(ctx, db, q.sql, func(id string, n int64) {
				if i, ok := idx[id]; ok {
					q.set(&snap.Libraries[i], n)
				}
			}); err != nil {
				return nil, err
			}
		}
		for _, s := range snap.Libraries {
			t := &snap.Totals
			t.Videos += s.Videos
			t.Hours += s.Hours
			t.Bytes += s.Bytes
			t.TranscribedVideos += s.TranscribedVideos
			t.TranscribedHours += s.TranscribedHours
			t.Series += s.Series
			t.Seasons += s.Seasons
			t.Tagged += s.Tagged
			t.Indexed += s.Indexed
		}
		snap.Totals.Hours, snap.Totals.TranscribedHours = round2(snap.Totals.Hours), round2(snap.Totals.TranscribedHours)
		return snap, nil
	}
}

func scanCounts(ctx context.Context, db *sql.DB, q string, fn func(string, int64)) error {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return err
		}
		fn(id, n)
	}
	return rows.Err()
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// libraryRootsSQL: every root of a live library, with the library's
// present videos.
const libraryRootsSQL = `
SELECT l.id::text, lr.path,
       (SELECT count(*) FROM videos v WHERE v.library_id = l.id AND v.deleted_at IS NULL)
  FROM library_roots lr
  JOIN libraries l ON l.id = lr.library_id
 WHERE l.deleted_at IS NULL
 ORDER BY l.id, lr.path`

// DBLibraryRoots reads the library roots for the share root.
func DBLibraryRoots(db *sql.DB) func(ctx context.Context) ([]LibraryRoot, error) {
	return func(ctx context.Context) ([]LibraryRoot, error) {
		rows, err := db.QueryContext(ctx, libraryRootsSQL)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		var out []LibraryRoot
		for rows.Next() {
			var r LibraryRoot
			if err := rows.Scan(&r.LibraryID, &r.Path, &r.Videos); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
}
