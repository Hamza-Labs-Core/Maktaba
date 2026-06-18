package watch

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// repo is the thin SQL accessor over watch_sessions / user_analytics_prefs
// (+ read joins to videos / playback_state / search_history). The
// interesting lifecycle logic lives in logic.go so it is unit-tested
// without a DB; this layer stays a pure data accessor.
//
// Placeholders are $N, which both lib/pq (Postgres) and modernc/sqlite
// accept — matching the videos/channels handlers. Writes pass explicit
// timestamps from the handler clock rather than relying on the DB now(),
// so behaviour is deterministic and dialect-independent.
type repo struct{ db *sql.DB }

// activeRow is the slice of an active session the heartbeat/stop paths
// need: the video's duration (to compute percent) and the running
// counters.
type activeRow struct {
	videoID       string
	videoDuration float64
	lastHeartbeat time.Time
	durationSec   int
	state         string
}

// ─── lifecycle ─────────────────────────────────────────────────────────

// trackingEnabled reports whether the user permits analytics collection.
// An absent prefs row means tracking is ON (the default) — so existing
// users are unaffected and opting out is an explicit upsert (Story 29.4).
func (r *repo) trackingEnabled(ctx context.Context, userID string) (bool, error) {
	var enabled bool
	err := r.db.QueryRowContext(ctx,
		`SELECT track_enabled FROM user_analytics_prefs WHERE user_id = $1`, userID).
		Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return enabled, nil
}

// insertStart opens a new active session.
func (r *repo) insertStart(ctx context.Context, s startRow) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO watch_sessions
		  (id, user_id, video_id, started_at, last_heartbeat,
		   duration_sec, percent_complete, state, device_type, platform, quality, ip_addr_hash)
		VALUES ($1,$2,$3,$4,$4,0,0,'active',$5,$6,$7,$8)`,
		s.id, s.userID, s.videoID, s.now, s.deviceType, s.platform, s.quality, s.ipHash)
	return err
}

type startRow struct {
	id, userID, videoID           string
	deviceType, platform, quality string
	ipHash                        string
	now                           time.Time
}

// loadActive fetches the session (owner-scoped) plus its video duration.
// Returns sql.ErrNoRows when the session does not exist or is not the
// caller's.
func (r *repo) loadActive(ctx context.Context, id, userID string) (activeRow, error) {
	var a activeRow
	var dur sql.NullFloat64
	err := r.db.QueryRowContext(ctx, `
		SELECT ws.video_id, COALESCE(v.duration_sec,0), ws.last_heartbeat,
		       ws.duration_sec, ws.state
		FROM watch_sessions ws
		LEFT JOIN videos v ON v.id = ws.video_id
		WHERE ws.id = $1 AND ws.user_id = $2`, id, userID).
		Scan(&a.videoID, &dur, &a.lastHeartbeat, &a.durationSec, &a.state)
	if err != nil {
		return activeRow{}, err
	}
	a.videoDuration = dur.Float64
	return a, nil
}

// applyHeartbeat advances an active session's counters. Scoped to
// state='active' so a heartbeat can never resurrect a closed session.
func (r *repo) applyHeartbeat(ctx context.Context, id string, lastHB time.Time, durationSec int, pct float64) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE watch_sessions
		SET last_heartbeat = $2, duration_sec = $3, percent_complete = $4
		WHERE id = $1 AND state = 'active'`, id, lastHB, durationSec, pct)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// stop closes an active session. Returns rows affected: 0 means it was
// already closed (idempotent stop).
func (r *repo) stop(ctx context.Context, id string, endedAt time.Time, state string, pct float64, durationSec int) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE watch_sessions
		SET ended_at = $2, last_heartbeat = $2, state = $3,
		    percent_complete = $4, duration_sec = $5
		WHERE id = $1 AND state = 'active'`, id, endedAt, state, pct, durationSec)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// sessionView projects the current state of a session for the response.
func (r *repo) sessionView(ctx context.Context, id, userID string) (SessionView, error) {
	var v SessionView
	err := r.db.QueryRowContext(ctx, `
		SELECT id, video_id, state, duration_sec, percent_complete
		FROM watch_sessions WHERE id = $1 AND user_id = $2`, id, userID).
		Scan(&v.SessionID, &v.VideoID, &v.State, &v.DurationSec, &v.PercentComplete)
	return v, err
}

// reapStale marks every active session whose last heartbeat predates
// cutoff as interrupted, closing it at its last heartbeat. Returns the
// number reaped.
func (r *repo) reapStale(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE watch_sessions
		SET state = 'interrupted', ended_at = last_heartbeat
		WHERE state = 'active' AND last_heartbeat < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// purgeOlderThan deletes sessions started before cutoff (retention,
// Story 29.6). Returns the number deleted.
func (r *repo) purgeOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM watch_sessions WHERE started_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ─── history (Story 29.2) ──────────────────────────────────────────────

// historyFilter is the optional inclusive date window on started_at.
type historyFilter struct {
	from, to *time.Time
	limit    int
	offset   int
}

func (r *repo) history(ctx context.Context, userID string, f historyFilter) ([]HistoryItem, error) {
	args := []any{userID}
	where := []string{"ws.user_id = $1"}
	if f.from != nil {
		args = append(args, *f.from)
		where = append(where, "ws.started_at >= "+pidx(len(args)))
	}
	if f.to != nil {
		args = append(args, *f.to)
		where = append(where, "ws.started_at <= "+pidx(len(args)))
	}
	args = append(args, f.limit, f.offset)
	limitIdx, offsetIdx := pidx(len(args)-1), pidx(len(args))

	q := `
		SELECT v.id, v.title, COALESCE(v.duration_sec,0),
		       COUNT(ws.id), COALESCE(SUM(ws.duration_sec),0),
		       COALESCE(MAX(ws.percent_complete),0), MAX(ws.started_at),
		       COALESCE(ps.position_sec,0), COALESCE(ps.completed,false)
		FROM watch_sessions ws
		JOIN videos v ON v.id = ws.video_id
		LEFT JOIN playback_state ps ON ps.user_id = ws.user_id AND ps.video_id = ws.video_id
		WHERE ` + strings.Join(where, " AND ") + `
		GROUP BY v.id, v.title, v.duration_sec, ps.position_sec, ps.completed
		ORDER BY MAX(ws.started_at) DESC
		LIMIT ` + limitIdx + ` OFFSET ` + offsetIdx

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryItem{}
	for rows.Next() {
		var it HistoryItem
		var last time.Time
		if err := rows.Scan(&it.VideoID, &it.Title, &it.DurationSec,
			&it.TimesWatched, &it.TotalWatchSec, &it.BestPercent, &last,
			&it.PositionSec, &it.Completed); err != nil {
			return nil, err
		}
		it.LastWatchedAt = last.UTC().Format(time.RFC3339)
		out = append(out, it)
	}
	return out, rows.Err()
}

// videoHistory returns the caller's aggregate + session list for one video.
func (r *repo) videoHistory(ctx context.Context, userID, videoID string) (VideoHistory, error) {
	var vh VideoHistory
	vh.VideoID = videoID
	var first, last time.Time
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(ws.id), COALESCE(SUM(ws.duration_sec),0),
		       COALESCE(MAX(ws.percent_complete),0),
		       MIN(ws.started_at), MAX(ws.started_at),
		       COALESCE(ps.position_sec,0), COALESCE(ps.completed,false)
		FROM watch_sessions ws
		LEFT JOIN playback_state ps ON ps.user_id = ws.user_id AND ps.video_id = ws.video_id
		WHERE ws.user_id = $1 AND ws.video_id = $2
		GROUP BY ps.position_sec, ps.completed`, userID, videoID).
		Scan(&vh.TimesWatched, &vh.TotalWatchSec, &vh.BestPercent,
			&first, &last, &vh.PositionSec, &vh.Completed)
	if errors.Is(err, sql.ErrNoRows) {
		vh.Sessions = []SessionStub{}
		return vh, nil
	}
	if err != nil {
		return VideoHistory{}, err
	}
	vh.FirstWatchedAt = first.UTC().Format(time.RFC3339)
	vh.LastWatchedAt = last.UTC().Format(time.RFC3339)

	rows, err := r.db.QueryContext(ctx, `
		SELECT started_at, ended_at, duration_sec, percent_complete, state
		FROM watch_sessions WHERE user_id = $1 AND video_id = $2
		ORDER BY started_at DESC`, userID, videoID)
	if err != nil {
		return VideoHistory{}, err
	}
	defer rows.Close()
	vh.Sessions = []SessionStub{}
	for rows.Next() {
		var s SessionStub
		var started time.Time
		var ended sql.NullTime
		if err := rows.Scan(&started, &ended, &s.DurationSec, &s.PercentComplete, &s.State); err != nil {
			return VideoHistory{}, err
		}
		s.StartedAt = started.UTC().Format(time.RFC3339)
		if ended.Valid {
			e := ended.Time.UTC().Format(time.RFC3339)
			s.EndedAt = &e
		}
		vh.Sessions = append(vh.Sessions, s)
	}
	return vh, rows.Err()
}

// deleteHistory removes the caller's sessions and resume point for one
// video, in a single transaction so history and Continue-Watching stay
// in lockstep.
func (r *repo) deleteHistory(ctx context.Context, userID, videoID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM watch_sessions WHERE user_id = $1 AND video_id = $2`, userID, videoID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM playback_state WHERE user_id = $1 AND video_id = $2`, userID, videoID); err != nil {
		return err
	}
	return tx.Commit()
}

// ─── activity + privacy (Story 29.4) ───────────────────────────────────

func (r *repo) watchedActivity(ctx context.Context, userID string, n int) ([]ActivityItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT video_id, started_at, percent_complete
		FROM watch_sessions WHERE user_id = $1
		ORDER BY started_at DESC LIMIT $2`, userID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActivityItem{}
	for rows.Next() {
		var videoID string
		var at time.Time
		var pct float64
		if err := rows.Scan(&videoID, &at, &pct); err != nil {
			return nil, err
		}
		out = append(out, ActivityItem{Kind: "watched", At: at.UTC(),
			Meta: map[string]any{"video_id": videoID, "percent_complete": pct}})
	}
	return out, rows.Err()
}

func (r *repo) searchedActivity(ctx context.Context, userID string, n int) ([]ActivityItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT query, last_used_at FROM search_history WHERE user_id = $1
		ORDER BY last_used_at DESC LIMIT $2`, userID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActivityItem{}
	for rows.Next() {
		var query string
		var at time.Time
		if err := rows.Scan(&query, &at); err != nil {
			return nil, err
		}
		out = append(out, ActivityItem{Kind: "searched", At: at.UTC(),
			Meta: map[string]any{"query": query}})
	}
	return out, rows.Err()
}

// getPrefs returns the user's tracking switch (absent ⇒ enabled).
func (r *repo) getPrefs(ctx context.Context, userID string) (PrivacySettings, error) {
	enabled, err := r.trackingEnabled(ctx, userID)
	return PrivacySettings{TrackEnabled: enabled}, err
}

// setPrefs upserts the user's tracking switch.
func (r *repo) setPrefs(ctx context.Context, userID string, enabled bool, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO user_analytics_prefs (user_id, track_enabled, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET
		  track_enabled = EXCLUDED.track_enabled,
		  updated_at    = EXCLUDED.updated_at`, userID, enabled, now)
	return err
}

// videoExists reports whether a video id is present (start validation).
func (r *repo) videoExists(ctx context.Context, videoID string) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx,
		`SELECT 1 FROM videos WHERE id = $1`, videoID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
