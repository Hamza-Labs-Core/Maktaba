package eventbus

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/lib/pq"
)

// pgNotifyChannel is the Postgres NOTIFY channel the slot-0061
// events_notify() trigger fires on. Never inline this string elsewhere
// (mirrors the canonical-channel-name convention in pipeline's
// db/pubsub.py).
const pgNotifyChannel = "ws.events"

// PostgresBackend is the production Backend: the durable `events`
// table (migration slot 0061) for Append/Replay/Get/Prune, and a
// pq.Listener on 'ws.events' for the per-replica LISTEN stream. It is
// the same lib/pq driver and *sql.DB pool the rest of the API uses;
// LISTEN needs its own connection (pq.Listener) because a connection
// blocked in LISTEN can't serve queries.
type PostgresBackend struct {
	db  *sql.DB
	dsn string
	log *slog.Logger
}

// NewPostgresBackend wraps an existing pool for the table operations
// and keeps the dsn for the dedicated pq.Listener connection. logger
// nil → slog.Default().
func NewPostgresBackend(db *sql.DB, dsn string, logger *slog.Logger) *PostgresBackend {
	if logger == nil {
		logger = slog.Default()
	}
	return &PostgresBackend{db: db, dsn: dsn, log: logger}
}

const (
	appendSQL = `INSERT INTO events (channel, type, payload, created_at)
	    VALUES ($1, $2, $3, $4) RETURNING id`

	replaySQL = `SELECT id, channel, type, payload, created_at
	    FROM events WHERE channel = $1 AND id > $2 ORDER BY id ASC`

	getSQL = `SELECT id, channel, type, payload, created_at
	    FROM events WHERE id = $1`

	pruneSQL = `DELETE FROM events WHERE created_at < $1`
)

// Append inserts the row; the slot-0061 AFTER INSERT trigger fires the
// bounded pg_notify('ws.events', {id,channel}) — fan-out is the DB's
// job, not the caller's, so application code cannot forget to publish.
func (p *PostgresBackend) Append(ctx context.Context, ev Event) (int64, error) {
	body, err := encodePayload(ev.Payload)
	if err != nil {
		return 0, err
	}
	at := ev.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var id int64
	err = p.db.QueryRowContext(ctx, appendSQL, ev.Channel, ev.Type, body, at.UTC()).Scan(&id)
	return id, err
}

// Replay scans events on channel with id > afterID, ascending. Backs
// the LISTEN-loop gap recovery and the on-connect last_event_id
// handshake (Story 19.2 AC3), served by the events_channel_id index.
func (p *PostgresBackend) Replay(ctx context.Context, channel string, afterID int64) ([]Event, error) {
	rows, err := p.db.QueryContext(ctx, replaySQL, channel, afterID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Get reads a single row by id (the LISTEN loop reads the full payload
// back after the bounded NOTIFY).
func (p *PostgresBackend) Get(ctx context.Context, id int64) (Event, bool, error) {
	e, err := scanEvent(p.db.QueryRowContext(ctx, getSQL, id).Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return Event{}, false, nil
		}
		return Event{}, false, err
	}
	return e, true, nil
}

// Listen opens a dedicated pq.Listener on 'ws.events' and streams the
// trigger's {id,channel} frames. The returned channel closes when ctx
// is done. A ListenerEventConnectionAttemptFailed is logged but
// non-fatal — the Bus gap-recovery scan reconciles any missed NOTIFY
// once the listener reconnects.
func (p *PostgresBackend) Listen(ctx context.Context) (<-chan Notification, error) {
	out := make(chan Notification, 256)
	l := pq.NewListener(p.dsn, 10*time.Second, time.Minute,
		func(_ pq.ListenerEventType, err error) {
			if err != nil {
				p.log.Warn("eventbus: pq.Listener event error (gap-recovery will reconcile)",
					"err", err, "event", "eventbus_listener_error")
			}
		})
	if err := l.Listen(pgNotifyChannel); err != nil {
		_ = l.Close()
		close(out)
		return out, err
	}
	go func() {
		defer close(out)
		defer func() { _ = l.Close() }()
		for {
			select {
			case <-ctx.Done():
				return
			case n := <-l.NotificationChannel():
				if n == nil {
					// reconnect tick: a Notification with no payload.
					// The Bus loop only fans out on real frames; the
					// next real NOTIFY's gap scan covers anything
					// missed during the disconnect.
					continue
				}
				var note Notification
				if err := json.Unmarshal([]byte(n.Extra), &note); err != nil {
					p.log.Warn("eventbus: malformed NOTIFY payload skipped",
						"err", err, "payload", n.Extra,
						"event", "eventbus_bad_notify")
					continue
				}
				select {
				case out <- note:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// Prune deletes events older than the cutoff (Story 19.2 AC3 7-day
// retention), served by the events_created_at index.
func (p *PostgresBackend) Prune(ctx context.Context, olderThan time.Time) (int, error) {
	res, err := p.db.ExecContext(ctx, pruneSQL, olderThan.UTC())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Close is a no-op for the pool (owned by main.go); per-Listen
// listeners are closed when their ctx ends.
func (p *PostgresBackend) Close() error { return nil }

// scanEvent decodes one events row from a Scan-style function (works
// for both *sql.Row and *sql.Rows).
func scanEvent(scan func(...any) error) (Event, error) {
	var (
		e    Event
		body []byte
	)
	if err := scan(&e.ID, &e.Channel, &e.Type, &body, &e.At); err != nil {
		return Event{}, err
	}
	e.Payload = decodePayload(body)
	return e, nil
}
