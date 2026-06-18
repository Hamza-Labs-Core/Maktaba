package analytics

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// VideoStats implements GET /api/videos/{id}/stats (Story 29.5).
// Aggregates are visible to any authenticated user; the per-user
// breakdown (viewers[]) is admin-only — a regular user never learns who
// else watched.
func (h *Handler) VideoStats(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	videoID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(videoID); err != nil {
		httperror.Write(w, r, httperror.BadRequest("id must be a uuid"))
		return
	}

	stats, err := h.repo().videoStats(r.Context(), videoID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load video stats"))
		return
	}
	if p.IsAdmin {
		viewers, err := h.repo().videoViewers(r.Context(), videoID)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("load video viewers"))
			return
		}
		stats.Viewers = viewers
	}
	common.WriteJSON(w, r, http.StatusOK, stats)
}

// videoStats returns the aggregate stats for one video. An unwatched
// video yields all-zeros (and a nil last_watched_at), still 200.
func (r *repo) videoStats(ctx context.Context, videoID string) (VideoStats, error) {
	var s VideoStats
	var last sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(DISTINCT user_id),
		       COALESCE(AVG(percent_complete),0),
		       COALESCE(AVG(duration_sec),0),
		       COALESCE(AVG(CASE WHEN state='completed' THEN 1.0 ELSE 0 END),0),
		       MAX(started_at)
		FROM watch_sessions WHERE video_id = $1`, videoID).
		Scan(&s.TotalViews, &s.UniqueViewers, &s.AvgCompletion,
			&s.AvgWatchSec, &s.CompletionRate, &last)
	if err != nil {
		return VideoStats{}, err
	}
	if last.Valid {
		v := last.Time.UTC().Format(time.RFC3339)
		s.LastWatchedAt = &v
	}
	return s, nil
}

// videoViewers returns the per-user breakdown (admin only).
func (r *repo) videoViewers(ctx context.Context, videoID string) ([]Viewer, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT ws.user_id, u.username, COUNT(*),
		       COALESCE(SUM(ws.duration_sec),0), COALESCE(MAX(ws.percent_complete),0),
		       MAX(ws.started_at)
		FROM watch_sessions ws
		JOIN users u ON u.id = ws.user_id
		WHERE ws.video_id = $1
		GROUP BY ws.user_id, u.username
		ORDER BY 4 DESC`, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Viewer{}
	for rows.Next() {
		var v Viewer
		var last time.Time
		if err := rows.Scan(&v.UserID, &v.Username, &v.TimesWatched,
			&v.TotalWatchSec, &v.BestPercent, &last); err != nil {
			return nil, err
		}
		v.LastWatchedAt = last.UTC().Format(time.RFC3339)
		out = append(out, v)
	}
	return out, rows.Err()
}
