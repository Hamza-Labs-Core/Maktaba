package analytics

import (
	"context"
	"database/sql"
	"strconv"
	"time"
)

// repo holds the aggregate reads. driver selects dialect-specific SQL for
// date bucketing / DOW-HOUR extraction (Postgres in production; SQLite
// for the unified home-server binary).
type repo struct {
	db     *sql.DB
	driver string // "postgres" | "sqlite"
}

func newRepo(db *sql.DB, driver string) *repo {
	if driver == "" {
		driver = "postgres"
	}
	return &repo{db: db, driver: driver}
}

func (r *repo) isSQLite() bool { return r.driver == "sqlite" }

// startedPredicate returns the optional "started_at >= $N" fragment and
// the seed args slice for a range.
func (r *repo) startedPredicate(rng Range) (where string, args []any) {
	if rng.HasLowerBound() {
		return " WHERE ws.started_at >= $1", []any{rng.Start}
	}
	return "", nil
}

// ─── Live ──────────────────────────────────────────────────────────────

func (r *repo) live(ctx context.Context, staleCutoff, now time.Time) ([]LiveSession, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT ws.id, ws.user_id, u.username, ws.video_id, v.title,
		       ws.started_at, ws.percent_complete, ws.device_type, ws.platform
		FROM watch_sessions ws
		JOIN users u  ON u.id = ws.user_id
		JOIN videos v ON v.id = ws.video_id
		WHERE ws.state = 'active' AND ws.last_heartbeat >= $1
		ORDER BY ws.started_at DESC
		LIMIT 200`, staleCutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LiveSession{}
	for rows.Next() {
		var s LiveSession
		var started time.Time
		var device, platform sql.NullString
		if err := rows.Scan(&s.SessionID, &s.UserID, &s.Username, &s.VideoID,
			&s.Title, &started, &s.PercentComplete, &device, &platform); err != nil {
			return nil, err
		}
		s.StartedAt = started.UTC().Format(time.RFC3339)
		s.ElapsedSec = int(now.Sub(started).Seconds())
		if s.ElapsedSec < 0 {
			s.ElapsedSec = 0
		}
		s.DeviceType = device.String
		s.Platform = platform.String
		out = append(out, s)
	}
	return out, rows.Err()
}

// ─── Summary ───────────────────────────────────────────────────────────

func (r *repo) summaryTotals(ctx context.Context, rng Range) (watchSec, sessions, viewers int64, completion float64, err error) {
	where, args := r.startedPredicate(rng)
	q := `SELECT COALESCE(SUM(duration_sec),0), COUNT(*),
	             COUNT(DISTINCT user_id),
	             COALESCE(AVG(CASE WHEN state='completed' THEN 1.0 ELSE 0 END),0)
	      FROM watch_sessions ws` + where
	err = r.db.QueryRowContext(ctx, q, args...).Scan(&watchSec, &sessions, &viewers, &completion)
	return
}

// breakdown groups by a session column (device_type|platform). NULLs are
// folded into an "unknown" label.
func (r *repo) breakdown(ctx context.Context, rng Range, col string) ([]CountStat, error) {
	where, args := r.startedPredicate(rng)
	q := `SELECT COALESCE(NULLIF(` + col + `,''),'unknown'), COUNT(*), COALESCE(SUM(duration_sec),0)
	      FROM watch_sessions ws` + where +
		` GROUP BY 1 ORDER BY 2 DESC`
	return r.scanCountStats(ctx, q, args)
}

func (r *repo) genres(ctx context.Context, rng Range, limit int) ([]CountStat, error) {
	args := []any{}
	where := ""
	if rng.HasLowerBound() {
		args = append(args, rng.Start)
		where = " AND ws.started_at >= $1"
	}
	args = append(args, limit)
	q := `SELECT t.name, COUNT(*), COALESCE(SUM(ws.duration_sec),0)
	      FROM watch_sessions ws
	      JOIN video_tags vt ON vt.video_id = ws.video_id
	      JOIN tags t        ON t.id = vt.tag_id
	      WHERE 1=1` + where + `
	      GROUP BY t.name ORDER BY 2 DESC LIMIT $` + strconv.Itoa(len(args))
	return r.scanCountStats(ctx, q, args)
}

func (r *repo) libraries(ctx context.Context, rng Range) ([]LabelStat, error) {
	args := []any{}
	where := ""
	if rng.HasLowerBound() {
		args = append(args, rng.Start)
		where = " AND ws.started_at >= $1"
	}
	q := `SELECT l.id, l.name, COUNT(*), COALESCE(SUM(ws.duration_sec),0)
	      FROM watch_sessions ws
	      JOIN videos v    ON v.id = ws.video_id
	      JOIN libraries l ON l.id = v.library_id
	      WHERE 1=1` + where + `
	      GROUP BY l.id, l.name ORDER BY 3 DESC`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LabelStat{}
	for rows.Next() {
		var s LabelStat
		if err := rows.Scan(&s.ID, &s.Label, &s.Sessions, &s.WatchSec); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *repo) scanCountStats(ctx context.Context, q string, args []any) ([]CountStat, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CountStat{}
	for rows.Next() {
		var s CountStat
		if err := rows.Scan(&s.Label, &s.Sessions, &s.WatchSec); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ─── Top videos ────────────────────────────────────────────────────────

func (r *repo) topVideos(ctx context.Context, rng Range, limit int) ([]TopVideo, error) {
	args := []any{}
	where := ""
	if rng.HasLowerBound() {
		args = append(args, rng.Start)
		where = " AND ws.started_at >= $1"
	}
	args = append(args, limit)
	q := `SELECT ws.video_id, v.title, COUNT(*),
	             COALESCE(SUM(ws.duration_sec),0), COUNT(DISTINCT ws.user_id)
	      FROM watch_sessions ws
	      JOIN videos v ON v.id = ws.video_id
	      WHERE 1=1` + where + `
	      GROUP BY ws.video_id, v.title
	      ORDER BY 3 DESC, 4 DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TopVideo{}
	for rows.Next() {
		var v TopVideo
		if err := rows.Scan(&v.VideoID, &v.Title, &v.Sessions, &v.WatchSec, &v.Viewers); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ─── Users ─────────────────────────────────────────────────────────────

func (r *repo) activeUsers(ctx context.Context, rng Range, limit int) ([]ActiveUser, error) {
	args := []any{}
	where := ""
	if rng.HasLowerBound() {
		args = append(args, rng.Start)
		where = " AND ws.started_at >= $1"
	}
	args = append(args, limit)
	q := `SELECT ws.user_id, u.username, COALESCE(SUM(ws.duration_sec),0),
	             COUNT(*), MAX(ws.started_at)
	      FROM watch_sessions ws
	      JOIN users u ON u.id = ws.user_id
	      WHERE 1=1` + where + `
	      GROUP BY ws.user_id, u.username
	      ORDER BY 3 DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActiveUser{}
	for rows.Next() {
		var u ActiveUser
		var last time.Time
		if err := rows.Scan(&u.UserID, &u.Username, &u.WatchSec, &u.Sessions, &last); err != nil {
			return nil, err
		}
		u.LastSeenAt = last.UTC().Format(time.RFC3339)
		out = append(out, u)
	}
	return out, rows.Err()
}

// ─── Activity time series + heatmap ────────────────────────────────────

// bucketExpr returns the dialect SQL that truncates started_at to the
// bucket, plus a Go formatter applied to the scanned label.
func (r *repo) bucketExpr(bucket string) string {
	if r.isSQLite() {
		switch bucket {
		case "week":
			return "strftime('%Y-W%W', started_at)"
		case "month":
			return "strftime('%Y-%m', started_at)"
		default:
			return "strftime('%Y-%m-%d', started_at)"
		}
	}
	// Postgres: date_trunc returns a timestamp; cast to text for a stable
	// label.
	switch bucket {
	case "week":
		return "to_char(date_trunc('week', started_at), 'IYYY-\"W\"IW')"
	case "month":
		return "to_char(date_trunc('month', started_at), 'YYYY-MM')"
	default:
		return "to_char(date_trunc('day', started_at), 'YYYY-MM-DD')"
	}
}

func (r *repo) series(ctx context.Context, rng Range, bucket string) ([]TimePoint, error) {
	where, args := r.startedPredicate(rng)
	expr := r.bucketExpr(bucket)
	q := `SELECT ` + expr + ` AS b, COALESCE(SUM(duration_sec),0), COUNT(*)
	      FROM watch_sessions ws` + where + `
	      GROUP BY b ORDER BY b ASC`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TimePoint{}
	for rows.Next() {
		var p TimePoint
		if err := rows.Scan(&p.Bucket, &p.WatchSec, &p.Sessions); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *repo) heatCells(ctx context.Context, rng Range) ([]HeatCell, error) {
	where, args := r.startedPredicate(rng)
	var dowExpr, hourExpr string
	if r.isSQLite() {
		dowExpr = "CAST(strftime('%w', started_at) AS INTEGER)"
		hourExpr = "CAST(strftime('%H', started_at) AS INTEGER)"
	} else {
		dowExpr = "CAST(EXTRACT(DOW FROM started_at) AS INTEGER)"
		hourExpr = "CAST(EXTRACT(HOUR FROM started_at) AS INTEGER)"
	}
	q := `SELECT ` + dowExpr + `, ` + hourExpr + `, COALESCE(SUM(duration_sec),0)
	      FROM watch_sessions ws` + where + `
	      GROUP BY 1, 2`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HeatCell{}
	for rows.Next() {
		var c HeatCell
		if err := rows.Scan(&c.Dow, &c.Hour, &c.WatchSec); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
