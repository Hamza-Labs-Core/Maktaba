package watch

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
)

// Plays from the stream session (Epic 44 story 44.3).
//
// Every player opens a stream session and posts its position to
// /api/stream/sessions/{id}/progress every few seconds; that post also records
// the play, one watch_sessions row per stream session (stream_session_id):
//
//   - the first post opens the row ('active'), with the app, its version,
//     platform and device from X-Maktaba-Client and the address hashed;
//   - each later post moves last_heartbeat and percent_complete and adds the
//     watched seconds (StreamCreditedSeconds: the wall time between two posts
//     while the position moved forward);
//   - a completed position closes it 'completed'; closing the stream session
//     closes it (StopStreamPlay); the reaper interrupts one gone quiet.
//
// Nothing is recorded for a person who turned tracking off.

// StreamProgress is one progress post of a stream session.
type StreamProgress struct {
	StreamSessionID string
	UserID          string // the session's owner
	VideoID         string
	PositionSec     float64
	// VideoDurationSec is the video's length (0: unknown)
	VideoDurationSec float64
	// Completed: the player says it finished, or the position is past 95 %
	Completed bool
	Client    client.Info
	IPHash    string
	Now       time.Time
}

// RecordStreamProgress records a progress post as the play of its stream
// session. It reads the previous position from playback_state, so it runs
// before the post's own position is stored there.
func RecordStreamProgress(ctx context.Context, db *sql.DB, p StreamProgress) error {
	r := &repo{db: db}
	enabled, err := r.trackingEnabled(ctx, p.UserID)
	if err != nil || !enabled {
		return err
	}
	pct := PercentComplete(p.PositionSec, p.VideoDurationSec)
	state := StateActive
	if p.Completed {
		state = StateCompleted
	}

	var id string
	var lastHB flexTime
	var watched int
	var cur string
	err = db.QueryRowContext(ctx, `
		SELECT id, last_heartbeat, duration_sec, state FROM watch_sessions
		WHERE stream_session_id = $1`, p.StreamSessionID).Scan(&id, &lastHB, &watched, &cur)
	if errors.Is(err, sql.ErrNoRows) {
		var ended any
		if state != StateActive {
			ended = p.Now
		}
		// two posts at once: the second finds the row the first wrote, and
		// counts as a later post next time
		_, err = db.ExecContext(ctx, `
			INSERT INTO watch_sessions
			  (id, user_id, video_id, started_at, last_heartbeat, ended_at,
			   duration_sec, percent_complete, state, device_type, platform,
			   ip_addr_hash, stream_session_id, client_app, client_version)
			VALUES ($1,$2,$3,$4,$4,$5,0,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT (stream_session_id) WHERE stream_session_id IS NOT NULL DO NOTHING`,
			uuid.NewString(), p.UserID, p.VideoID, p.Now, ended, pct, state,
			p.Client.Device, p.Client.Platform, p.IPHash, p.StreamSessionID,
			p.Client.App, p.Client.Version)
		return err
	}
	if err != nil {
		return err
	}
	if cur != StateActive {
		return nil // closed: a post after the close never reopens it
	}

	var prevPos float64
	err = db.QueryRowContext(ctx, `
		SELECT position_sec FROM playback_state WHERE user_id = $1 AND video_id = $2`,
		p.UserID, p.VideoID).Scan(&prevPos)
	if errors.Is(err, sql.ErrNoRows) {
		prevPos, err = p.PositionSec, nil // nothing to measure against: adds nothing
	}
	if err != nil {
		return err
	}
	watched += StreamCreditedSeconds(lastHB.t, p.Now, prevPos, p.PositionSec, DefaultStaleTimeout)

	var ended any
	if state != StateActive {
		ended = p.Now
	}
	_, err = db.ExecContext(ctx, `
		UPDATE watch_sessions
		SET last_heartbeat = $2, duration_sec = $3, percent_complete = $4,
		    state = $5, ended_at = $6
		WHERE id = $1 AND state = 'active'`, id, p.Now, watched, pct, state, ended)
	return err
}

// StopStreamPlay closes the play of a stream session that was closed: it ends
// 'completed' when its position was past 95 %, else 'stopped'. A session
// without a play (tracking off, no progress yet) or one already closed is left
// as it is.
func StopStreamPlay(ctx context.Context, db *sql.DB, streamSessionID string, now time.Time) error {
	_, err := db.ExecContext(ctx, `
		UPDATE watch_sessions
		SET ended_at = $2,
		    state = CASE WHEN percent_complete >= $3 THEN 'completed' ELSE 'stopped' END
		WHERE stream_session_id = $1 AND state = 'active'`, streamSessionID, now, completedThreshold)
	return err
}

// flexTime scans a timestamp from either database: Postgres hands a
// time.Time, SQLite (whose columns are TEXT) the text it stored.
type flexTime struct{ t time.Time }

// the layouts SQLite's text may be in: the driver's own (a time.Time it was
// given) and datetime('now')'s
var flexLayouts = []string{
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999-07:00",
	time.RFC3339Nano,
	"2006-01-02 15:04:05",
}

func (f *flexTime) Scan(v any) error {
	switch x := v.(type) {
	case time.Time:
		f.t = x
		return nil
	case string:
		return f.parse(x)
	case []byte:
		return f.parse(string(x))
	}
	return errors.New("watch: unreadable timestamp")
}

func (f *flexTime) parse(s string) error {
	for _, l := range flexLayouts {
		if t, err := time.Parse(l, s); err == nil {
			f.t = t
			return nil
		}
	}
	return errors.New("watch: unreadable timestamp " + s)
}

// HashIP returns a salted, truncated SHA-256 of the client's address (D4). The
// router runs chi's RealIP, so RemoteAddr is the resolved source.
func HashIP(salt string, r *http.Request) string {
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		ip = host
	}
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(salt + "|" + ip))
	return hex.EncodeToString(sum[:])[:16]
}
