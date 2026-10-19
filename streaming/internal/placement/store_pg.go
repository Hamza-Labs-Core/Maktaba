package placement

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore is the Postgres Store (slot 0123).
type PGStore struct {
	Pool *pgxpool.Pool
}

const placementCols = `channel_id, host_id, generation, state, reason, placed_at, acked_at,
	EXTRACT(EPOCH FROM (now() - placed_at))::float8`

type rowScanner interface{ Scan(dest ...any) error }

func scanPlacement(r rowScanner) (Placement, error) {
	var p Placement
	var age float64
	if err := r.Scan(&p.Channel, &p.Host, &p.Gen, &p.State, &p.Reason, &p.PlacedAt, &p.AckedAt, &age); err != nil {
		return Placement{}, err
	}
	p.Age = time.Duration(age * float64(time.Second))
	return p, nil
}

func (s PGStore) Hosts(ctx context.Context) ([]Host, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT host_id, host, role, COALESCE(stream_base_url, ''), enabled, draining,
		       COALESCE(capacity_override, max_active), weight, can_live, COALESCE(unfit, ''),
		       COALESCE((load->>'protocol')::int, 0),
		       EXTRACT(EPOCH FROM (now() - heartbeat_at))::float8, encoders::text
		FROM streaming_hosts ORDER BY host_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Host
	for rows.Next() {
		var h Host
		var age float64
		var encs string
		if err := rows.Scan(&h.ID, &h.Name, &h.Role, &h.BaseURL, &h.Enabled, &h.Draining, &h.Capacity,
			&h.Weight, &h.CanLive, &h.Unfit, &h.Protocol, &age, &encs); err != nil {
			return nil, err
		}
		h.BeatAge = time.Duration(age * float64(time.Second))
		_ = json.Unmarshal([]byte(encs), &h.Encoders)
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s PGStore) Get(ctx context.Context, ch uuid.UUID) (Placement, bool, error) {
	p, err := scanPlacement(s.Pool.QueryRow(ctx, `SELECT `+placementCols+` FROM channel_placements WHERE channel_id = $1`, ch))
	if errors.Is(err, pgx.ErrNoRows) {
		return Placement{}, false, nil
	}
	if err != nil {
		return Placement{}, false, err
	}
	return p, true, nil
}

func notifyPayload(p Placement) string {
	return p.Channel.String() + ":" + p.Host + ":" + strconv.FormatInt(p.Gen, 10)
}

func (s PGStore) Place(ctx context.Context, ch uuid.UUID, decide func(cur *Placement, used map[string]int) (Decision, error)) (Placement, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Placement{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// the channel's decision, then the cluster's capacity: two first requests for
	// different channels never overfill one host
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('tv_place:' || $1::text))`, ch.String()); err != nil {
		return Placement{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('tv_place_capacity'))`); err != nil {
		return Placement{}, err
	}
	var cur *Placement
	p, err := scanPlacement(tx.QueryRow(ctx, `SELECT `+placementCols+` FROM channel_placements WHERE channel_id = $1`, ch))
	switch {
	case err == nil:
		cur = &p
	case !errors.Is(err, pgx.ErrNoRows):
		return Placement{}, err
	}
	used := map[string]int{}
	rows, err := tx.Query(ctx, `SELECT host_id, count(*) FROM channel_placements
		WHERE state IN ('placing','live','moving') AND channel_id <> $1 GROUP BY host_id`, ch)
	if err != nil {
		return Placement{}, err
	}
	for rows.Next() {
		var h string
		var n int
		if err := rows.Scan(&h, &n); err != nil {
			rows.Close()
			return Placement{}, err
		}
		used[h] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Placement{}, err
	}
	d, err := decide(cur, used)
	if err != nil {
		return Placement{}, err
	}
	if d.Keep && cur != nil {
		return *cur, tx.Commit(ctx)
	}
	state := StatePlacing
	if d.Reason == ReasonMove || d.Reason == ReasonDrain {
		state = StateMoving
	}
	np, err := scanPlacement(tx.QueryRow(ctx, `
		INSERT INTO channel_placements (channel_id, host_id, generation, state, reason, placed_at)
		VALUES ($1, $2, nextval('tv_placement_generation_seq'), $3, $4, now())
		ON CONFLICT (channel_id) DO UPDATE SET host_id = EXCLUDED.host_id,
			generation = EXCLUDED.generation, state = EXCLUDED.state, reason = EXCLUDED.reason,
			placed_at = now(), acked_at = NULL, released_at = NULL
		RETURNING `+placementCols, ch, d.Host, state, d.Reason))
	if err != nil {
		return Placement{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('tv_placement', $1)`, notifyPayload(np)); err != nil {
		return Placement{}, err
	}
	return np, tx.Commit(ctx)
}

func (s PGStore) Stop(ctx context.Context, ch uuid.UUID) (Placement, bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Placement{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('tv_place:' || $1::text))`, ch.String()); err != nil {
		return Placement{}, false, err
	}
	p, err := scanPlacement(tx.QueryRow(ctx, `
		UPDATE channel_placements SET state = 'released', released_at = now(),
			generation = nextval('tv_placement_generation_seq')
		WHERE channel_id = $1 AND state <> 'released'
		RETURNING `+placementCols, ch))
	if errors.Is(err, pgx.ErrNoRows) {
		return Placement{}, false, tx.Commit(ctx)
	}
	if err != nil {
		return Placement{}, false, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('tv_placement', $1)`, notifyPayload(p)); err != nil {
		return Placement{}, false, err
	}
	return p, true, tx.Commit(ctx)
}

func (s PGStore) Ack(ctx context.Context, ch uuid.UUID, gen int64) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT tv_placement_ack($1, $2)`, ch, gen).Scan(&ok)
	return ok, err
}

func (s PGStore) Release(ctx context.Context, ch uuid.UUID, gen int64) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT tv_placement_release($1, $2)`, ch, gen).Scan(&ok)
	return ok, err
}

func (s PGStore) list(ctx context.Context, q string, args ...any) ([]Placement, error) {
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Placement
	for rows.Next() {
		p, err := scanPlacement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s PGStore) OnHost(ctx context.Context, host string) ([]Placement, error) {
	return s.list(ctx, `SELECT `+placementCols+` FROM channel_placements
		WHERE host_id = $1 AND state IN ('placing','live','moving')`, host)
}

func (s PGStore) All(ctx context.Context) ([]Placement, error) {
	return s.list(ctx, `SELECT `+placementCols+` FROM channel_placements`)
}

func (s PGStore) ChannelSlug(ctx context.Context, ch uuid.UUID) (string, error) {
	var slug string
	err := s.Pool.QueryRow(ctx, `SELECT slug FROM channels WHERE id = $1`, ch).Scan(&slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return slug, err
}

func (s PGStore) HasLiveBlocks(ctx context.Context, ch uuid.UUID, within time.Duration) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM channel_programs_live
		WHERE channel_id = $1 AND kind = 'live' AND end_at > now()
		  AND start_at < now() + make_interval(secs => $2))`, ch, within.Seconds()).Scan(&ok)
	return ok, err
}

// CleanStaleRuntime deletes channel_runtime rows that describe nothing on air
// (story 32.5 AC7): no active placement for that channel on that host and no
// heartbeat for over a minute, or a host that has been down for over a minute.
func (s PGStore) CleanStaleRuntime(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `
		DELETE FROM channel_runtime r
		WHERE (r.heartbeat_at IS NULL OR r.heartbeat_at < now() - interval '1 minute')
		  AND (NOT EXISTS (SELECT 1 FROM channel_placements p
		                   WHERE p.channel_id = r.channel_id AND p.host_id = r.host_id
		                     AND p.state IN ('placing','live','moving'))
		       OR EXISTS (SELECT 1 FROM streaming_hosts h WHERE h.host_id = r.host_id
		                  AND h.heartbeat_at < now() - interval '1 minute'))`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// OnAirPG counts the channels on air per host and encoder from
// channel_runtime (a heartbeat within 45 s), for the metrics (story 32.11).
func OnAirPG(pool *pgxpool.Pool) OnAirCounter {
	return func(ctx context.Context) (map[[2]string]int, error) {
		rows, err := pool.Query(ctx, `SELECT COALESCE(host_id, host), COALESCE(encoder, ''), count(*)
			FROM channel_runtime
			WHERE state <> 'idle' AND COALESCE(heartbeat_at, last_segment_at) > now() - interval '45 seconds'
			GROUP BY 1, 2`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := map[[2]string]int{}
		for rows.Next() {
			var host, enc string
			var n int
			if err := rows.Scan(&host, &enc, &n); err != nil {
				return nil, err
			}
			out[[2]string{host, enc}] = n
		}
		return out, rows.Err()
	}
}
