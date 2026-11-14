package broadcast

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore reads the published channels and their tokens (slot 0226). The front door runs
// as the home's own database user; a machine's login cannot read broadcast_ingest_tokens.
type PGStore struct{ Pool *pgxpool.Pool }

// Available reports whether slot 0226 exists (the API migrates it, maybe after this
// process started).
func (s PGStore) Available(ctx context.Context) bool {
	var ok bool
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.Pool.QueryRow(qctx, `SELECT to_regclass('broadcast_ingest_tokens') IS NOT NULL`).Scan(&ok) == nil && ok
}

// Jobs implements Store: each published channel with a token, its ladder less the
// rungs the uplink dropped.
func (s PGStore) Jobs(ctx context.Context) ([]Job, error) {
	rows, err := s.Pool.Query(ctx, `SELECT b.channel_id, c.name, b.rungs, b.dropped_rungs, b.mode, b.state,
			t.token, t.ingest_url, t.edge_channel, t.expires_at
		FROM broadcast_channels b
		JOIN channels c ON c.id = b.channel_id
		JOIN broadcast_ingest_tokens t ON t.channel_id = b.channel_id
		WHERE b.state IN ('live', 'offline', 'off_air') AND b.cloud_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		var raw []byte
		var dropped int
		if err := rows.Scan(&j.Channel, &j.Name, &raw, &dropped, &j.Mode, &j.State,
			&j.Creds.Token, &j.Creds.IngestURL, &j.Creds.Channel, &j.Creds.ExpiresAt); err != nil {
			return nil, err
		}
		var rungs []string
		_ = json.Unmarshal(raw, &rungs)
		j.Rungs = Effective(rungs, dropped)
		out = append(out, j)
	}
	return out, rows.Err()
}

// Effective is a ladder less its top dropped rungs, keeping at least the lowest (the
// API's broadcast.Effective).
func Effective(rungs []string, dropped int) []string {
	if dropped <= 0 || len(rungs) == 0 {
		return rungs
	}
	if dropped >= len(rungs) {
		return rungs[len(rungs)-1:]
	}
	return rungs[dropped:]
}

// SetState implements Store.
func (s PGStore) SetState(ctx context.Context, ch uuid.UUID, state, detail string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE broadcast_channels SET state = $2, detail = $3, updated_at = now()
		WHERE channel_id = $1 AND cloud_id IS NOT NULL AND state <> 'draft'`, ch, state, detail)
	return err
}

// Report implements Store.
func (s PGStore) Report(ctx context.Context, ch uuid.UUID, report map[string]any) error {
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE broadcast_channels SET report = $2::jsonb, reported_at = now() WHERE channel_id = $1`, ch, raw)
	return err
}
