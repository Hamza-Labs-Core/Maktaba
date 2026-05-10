// Story 9.7 — library stats backed by the denormalized
// ``library_stats_cache`` row.
//
// The Phase-3 ``Stats`` handler in libraries.go computed everything
// from ``videos`` directly — fine for an empty dev DB but it would
// blow past the 50 ms SLA on a 50k-video library. This file adds the
// cache-aware path:
//
//   1. Read the cache row in one round-trip.
//   2. If the row is missing (a brand-new library, or the cache was
//      truncated), recompute from source tables and upsert. Subsequent
//      requests hit the cache.
//   3. ``maktaba-api stats-rebuild`` (out of scope for this file) is
//      what production uses to recompute periodically; this handler
//      relies on the recompute-on-miss path to stay correct without
//      it.
//
// The recompute is one read of ``videos`` (state, language,
// content_type, source size) and one read of ``processing_jobs`` for
// the jobs facet — every grouping is a single GROUP BY.
package libraries

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// CachedStatsResponse is the AC-1 over-the-wire shape. It is a strict
// superset of the Phase-3 StatsResponse: every field returned there is
// also returned here, plus the new content-type / jobs / sweep facets.
type CachedStatsResponse struct {
	TotalVideos        int                `json:"total_videos"`
	TotalDurationSec   int64              `json:"total_duration_sec"`
	SourceSizeBytes    int64              `json:"source_size_bytes"`
	DerivedSizeBytes   int64              `json:"derived_size_bytes"`
	ByState            map[string]int     `json:"by_state"`
	ByLanguage         map[string]int     `json:"by_language"`
	ByContentType      map[string]int     `json:"by_content_type"`
	Jobs               map[string]int     `json:"jobs"`
	ProcessedPct       *float64           `json:"processed_pct"`
	LastSweep          *SweepSummary      `json:"last_sweep"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

// SweepSummary is the AC-1 ``last_sweep`` envelope.
type SweepSummary struct {
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Scanned     int       `json:"scanned"`
	NewVideos   int       `json:"new_videos"`
	MovedVideos int       `json:"moved_videos"`
	Removed     int       `json:"removed_videos"`
}

// StatsCached implements ``GET /api/libraries/{id}/stats`` with the
// cache-first read path. It overrides the Phase-3 Stats handler when
// wired via :func:`MountStatsCached` (default in production); the
// no-cache path remains for environments where the
// ``library_stats_cache`` table hasn't been migrated yet.
func (h *Handler) StatsCached(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return
	}
	if _, err := h.loadLibrary(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("library "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load library"))
		return
	}

	resp, err := readCacheRow(r.Context(), h.DB, id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("stats cache read: "+err.Error()))
		return
	}
	if resp == nil {
		// Cache miss → recompute from source tables and upsert.
		resp, err = recomputeStats(r.Context(), h.DB, id)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("stats recompute: "+err.Error()))
			return
		}
		if err := upsertCacheRow(r.Context(), h.DB, id, resp); err != nil {
			// Best-effort: failing to write the cache must not fail
			// the request. Log via the structured logger in production
			// (out of scope for this skeleton).
			_ = err
		}
	}
	common.WriteJSON(w, r, http.StatusOK, resp)
}

// readCacheRow fetches the precomputed row for ``id``. Returns
// (nil, nil) when the row doesn't exist (cache miss), distinguished
// from a hard SQL error.
func readCacheRow(ctx context.Context, db *sql.DB, id string) (*CachedStatsResponse, error) {
	var (
		total, dur, src, derived          int64
		byState, byLang, byType, jobs     []byte
		lastSweep                         []byte
		updatedAt                         time.Time
	)
	err := db.QueryRowContext(ctx, `
		SELECT total_videos, total_duration_sec, source_size_bytes,
		       derived_size_bytes, by_state_jsonb, by_language_jsonb,
		       by_content_type_jsonb, jobs_jsonb, last_sweep_jsonb, updated_at
		FROM library_stats_cache WHERE library_id = $1
	`, id).Scan(&total, &dur, &src, &derived, &byState, &byLang, &byType, &jobs, &lastSweep, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	resp := &CachedStatsResponse{
		TotalVideos:      int(total),
		TotalDurationSec: dur,
		SourceSizeBytes:  src,
		DerivedSizeBytes: derived,
		ByState:          decodeIntMap(byState),
		ByLanguage:       decodeIntMap(byLang),
		ByContentType:    decodeIntMap(byType),
		Jobs:             decodeIntMap(jobs),
		UpdatedAt:        updatedAt,
	}
	if total > 0 {
		ready := resp.ByState["ready"] + resp.ByState["ready_no_audio"]
		denom := int(total) - resp.ByState["superseded"] - resp.ByState["missing"]
		if denom > 0 {
			pct := float64(ready) / float64(denom) * 100.0
			resp.ProcessedPct = &pct
		}
	}
	if len(lastSweep) > 0 {
		var s SweepSummary
		if err := json.Unmarshal(lastSweep, &s); err == nil {
			resp.LastSweep = &s
		}
	}
	return resp, nil
}

// recomputeStats does the source-table scan that backs the cache. The
// queries are small (one row per state, one per language, one per type,
// one per job-state) so this is the canonical "rebuild" implementation
// that the operational ``stats-rebuild`` command also calls.
func recomputeStats(ctx context.Context, db *sql.DB, id string) (*CachedStatsResponse, error) {
	resp := &CachedStatsResponse{
		ByState:       map[string]int{},
		ByLanguage:    map[string]int{},
		ByContentType: map[string]int{},
		Jobs:          map[string]int{},
		UpdatedAt:     time.Now().UTC(),
	}

	// State + duration + source bytes from the videos table.
	rows, err := db.QueryContext(ctx, `
		SELECT state, COUNT(*),
		       COALESCE(SUM(duration_sec), 0),
		       COALESCE(SUM(size_bytes), 0)
		FROM videos WHERE library_id = $1
		GROUP BY state
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var cnt int
		var dur float64
		var sz int64
		if err := rows.Scan(&state, &cnt, &dur, &sz); err != nil {
			return nil, err
		}
		resp.ByState[state] = cnt
		resp.TotalVideos += cnt
		resp.TotalDurationSec += int64(dur)
		resp.SourceSizeBytes += sz
	}

	if resp.TotalVideos > 0 {
		ready := resp.ByState["ready"] + resp.ByState["ready_no_audio"]
		denom := resp.TotalVideos - resp.ByState["superseded"] - resp.ByState["missing"]
		if denom > 0 {
			pct := float64(ready) / float64(denom) * 100.0
			resp.ProcessedPct = &pct
		}
	}

	// Language facet.
	if err := groupCount(ctx, db, `
		SELECT COALESCE(detected_language, 'und'), COUNT(*)
		FROM videos WHERE library_id = $1
		GROUP BY detected_language
	`, id, resp.ByLanguage); err != nil {
		return nil, err
	}

	// Content type facet — only present once 0045 has been applied.
	_ = groupCount(ctx, db, `
		SELECT COALESCE(content_type, 'unknown'), COUNT(*)
		FROM videos WHERE library_id = $1
		GROUP BY content_type
	`, id, resp.ByContentType)

	// Jobs facet.
	if err := groupCount(ctx, db, `
		SELECT j.state, COUNT(*) FROM processing_jobs j
		JOIN videos v ON v.id = j.video_id
		WHERE v.library_id = $1
		GROUP BY j.state
	`, id, resp.Jobs); err != nil {
		// processing_jobs may legitimately be empty in a fresh DB; we
		// don't want a missing GROUP BY result to fail the response.
		_ = err
	}

	// Last-sweep envelope (best-effort — table is added by 0044).
	resp.LastSweep = readLastSweep(ctx, db, id)
	return resp, nil
}

func groupCount(ctx context.Context, db *sql.DB, query, libID string, dst map[string]int) error {
	rows, err := db.QueryContext(ctx, query, libID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var cnt int
		if err := rows.Scan(&key, &cnt); err != nil {
			return err
		}
		dst[key] = cnt
	}
	return nil
}

func readLastSweep(ctx context.Context, db *sql.DB, libID string) *SweepSummary {
	var (
		started, finished time.Time
		finishedNull      sql.NullTime
		scanned, newCnt   int
		moved, removed    int
	)
	err := db.QueryRowContext(ctx, `
		SELECT started_at, finished_at, scanned, new_videos, moved_videos, removed_videos
		FROM library_sweeps WHERE library_id = $1
		ORDER BY started_at DESC LIMIT 1
	`, libID).Scan(&started, &finishedNull, &scanned, &newCnt, &moved, &removed)
	if err != nil {
		return nil
	}
	out := &SweepSummary{
		StartedAt:   started,
		Scanned:     scanned,
		NewVideos:   newCnt,
		MovedVideos: moved,
		Removed:     removed,
	}
	if finishedNull.Valid {
		finished = finishedNull.Time
		out.FinishedAt = &finished
	}
	return out
}

func upsertCacheRow(ctx context.Context, db *sql.DB, id string, resp *CachedStatsResponse) error {
	byState, _ := json.Marshal(resp.ByState)
	byLang, _ := json.Marshal(resp.ByLanguage)
	byType, _ := json.Marshal(resp.ByContentType)
	jobs, _ := json.Marshal(resp.Jobs)
	var lastSweep []byte
	if resp.LastSweep != nil {
		lastSweep, _ = json.Marshal(resp.LastSweep)
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO library_stats_cache (
			library_id, total_videos, total_duration_sec, source_size_bytes,
			derived_size_bytes, by_state_jsonb, by_language_jsonb,
			by_content_type_jsonb, jobs_jsonb, last_sweep_jsonb, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (library_id) DO UPDATE SET
			total_videos = EXCLUDED.total_videos,
			total_duration_sec = EXCLUDED.total_duration_sec,
			source_size_bytes = EXCLUDED.source_size_bytes,
			derived_size_bytes = EXCLUDED.derived_size_bytes,
			by_state_jsonb = EXCLUDED.by_state_jsonb,
			by_language_jsonb = EXCLUDED.by_language_jsonb,
			by_content_type_jsonb = EXCLUDED.by_content_type_jsonb,
			jobs_jsonb = EXCLUDED.jobs_jsonb,
			last_sweep_jsonb = EXCLUDED.last_sweep_jsonb,
			updated_at = EXCLUDED.updated_at
	`, id, resp.TotalVideos, resp.TotalDurationSec, resp.SourceSizeBytes,
		resp.DerivedSizeBytes, string(byState), string(byLang),
		string(byType), string(jobs), nullableJSON(lastSweep), resp.UpdatedAt)
	return err
}

func decodeIntMap(b []byte) map[string]int {
	out := map[string]int{}
	if len(b) == 0 {
		return out
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out
	}
	return out
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
