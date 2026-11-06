package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Restart requests (slot 0185, #636): POST /api/components/{id}/restart restarts the
// supervisor's processes and the API's loops itself; a loop that runs in another process
// (streaming, the pipeline) is that process's runner's, so the API writes a request here
// and that runner restarts it. A row is (machine, component, host) with a seq that moves
// on every request; a write moves the revision (and notifies), so the runners hear it.

// RequestRestart asks host's runners on machine to restart their loops of id; the
// request's new seq.
func (s *Store) RequestRestart(ctx context.Context, machine, id, host, by string) (int64, error) {
	if machine == "" {
		machine = Home
	}
	if id == "" || host == "" {
		return 0, fmt.Errorf("components: a restart request needs a component and a host")
	}
	var seq int64
	err := s.DB.QueryRowContext(ctx, `INSERT INTO component_restarts AS r (machine, id, host, seq, requested_at, requested_by)
		VALUES ($1, $2, $3, 1, $4, $5)
		ON CONFLICT (machine, id, host) DO UPDATE SET seq = r.seq + 1,
			requested_at = excluded.requested_at, requested_by = excluded.requested_by
		RETURNING seq`, machine, id, host, ts(s.DB, time.Now()), by).Scan(&seq)
	return seq, err
}

// Restarts are the restart requests of host on machine: component id -> seq.
func (s *Store) Restarts(ctx context.Context, machine, host string) (map[string]int64, error) {
	if machine == "" {
		machine = Home
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, seq FROM component_restarts WHERE machine = $1 AND host = $2`, machine, host)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var seq sql.NullInt64
		if err := rows.Scan(&id, &seq); err != nil {
			return nil, err
		}
		out[id] = seq.Int64
	}
	return out, rows.Err()
}
