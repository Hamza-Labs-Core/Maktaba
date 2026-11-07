package billing

import (
	"context"
	"database/sql"
	"time"
)

// StreamSilence is how long a relayed stream may go without a segment before it no longer
// counts (story 50.19 §3): the worker deletes it then, and counting ignores it already.
const StreamSilence = 2 * time.Minute

// StreamLimit is how many relayed streams a plan plays at once, with its extra streams; ok
// false: no limit (a plan without the relay never streams; a count not set yet, TBD, is not
// enforced; a plan this build doesn't know is never cut off by it).
func StreamLimit(plan string, extra int) (limit int, ok bool) {
	t, known := Tiers[plan]
	if !known || t.MaxRelayStreams == nil {
		return 0, false
	}
	n := *t.MaxRelayStreams
	if t.ExtraStreamsAddOn && extra > 0 {
		n += extra
	}
	return n, true
}

// Streams counts each home's open relayed streams in Postgres (relay_streams), so the blue and
// green relay processes of a deploy (story 50.04) agree.
type Streams struct {
	DB *sql.DB
	// Relay names this process in its rows (relay.id).
	Relay string
	now   func() time.Time
}

func (s *Streams) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Open admits a stream of a home: one already open is touched and admitted; a new one is
// admitted while fewer than limit streams of the home were seen in the last StreamSilence,
// and counted. It answers whether it is admitted and how many the home has open. The count
// and the insert hold the home's advisory lock, so two relays never both admit the last slot.
func (s *Streams) Open(ctx context.Context, serverID, session string, limit int) (admitted bool, open int, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('relay_streams:' || $1))`, serverID); err != nil {
		return false, 0, err
	}
	now := s.clock()
	res, err := tx.ExecContext(ctx, `
        UPDATE relay_streams SET last_seen_at = $3, relay = $4
         WHERE server_id = $1 AND session = $2 AND last_seen_at > $5`,
		serverID, session, now, s.Relay, now.Add(-StreamSilence))
	if err != nil {
		return false, 0, err
	}
	if err := tx.QueryRowContext(ctx, `
        SELECT count(*) FROM relay_streams WHERE server_id = $1 AND last_seen_at > $2`,
		serverID, now.Add(-StreamSilence)).Scan(&open); err != nil {
		return false, 0, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, open, tx.Commit()
	}
	if open >= limit {
		return false, open, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO relay_streams (server_id, session, relay, opened_at, last_seen_at) VALUES ($1, $2, $3, $4, $4)
        ON CONFLICT (server_id, session) DO UPDATE SET relay = EXCLUDED.relay, opened_at = EXCLUDED.opened_at,
               last_seen_at = EXCLUDED.last_seen_at`,
		serverID, session, s.Relay, now); err != nil {
		return false, 0, err
	}
	return true, open + 1, tx.Commit()
}

// Sweep deletes the streams silent for StreamSilence (the worker, every minute).
func (s *Streams) Sweep(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM relay_streams WHERE last_seen_at <= $1`, s.clock().Add(-StreamSilence))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
