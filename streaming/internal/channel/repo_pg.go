package channel

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGRepo is the production Repo over the streaming service's pgx pool. It
// reads channel_programs (slot 0082) resolving each block's video to its
// on-disk path, and maintains channel_runtime (slot 0083).
//
// The `live` kind and the nullable `source_url` column arrive in a later
// migration; the repo checks information_schema (re-checking every few
// minutes while absent) so it works on both schemas.
type PGRepo struct {
	pool *pgxpool.Pool
	host string

	mu            sync.Mutex
	hasSourceURL  bool
	checkedSrcURL time.Time
}

// NewPGRepo builds a PGRepo. `host` is the pinned host written to
// channel_runtime (§4.2).
func NewPGRepo(pool *pgxpool.Pool, host string) *PGRepo {
	return &PGRepo{pool: pool, host: host}
}

const sourceURLRecheck = 5 * time.Minute

// sourceURLColumn reports whether channel_programs.source_url exists.
func (r *PGRepo) sourceURLColumn(ctx context.Context) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hasSourceURL || (!r.checkedSrcURL.IsZero() && time.Since(r.checkedSrcURL) < sourceURLRecheck) {
		return r.hasSourceURL
	}
	var ok bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'channel_programs' AND column_name = 'source_url'
			  AND table_schema = ANY (current_schemas(false))
		)`).Scan(&ok)
	if err == nil {
		r.hasSourceURL = ok
		r.checkedSrcURL = time.Now()
	}
	return r.hasSourceURL
}

func (r *PGRepo) ProgramsFrom(ctx context.Context, channelID uuid.UUID, from time.Time, limit int) ([]ProgramBlock, error) {
	srcCol := "NULL::text"
	if r.sourceURLColumn(ctx) {
		srcCol = "cp.source_url"
	}
	rows, err := r.pool.Query(ctx, `
		SELECT cp.seq, cp.kind, cp.video_id, cp.start_at, cp.end_at,
		       cp.source_offset, cp.source_duration, v.path, `+srcCol+`
		FROM channel_programs cp
		LEFT JOIN videos v ON v.id = cp.video_id
		WHERE cp.channel_id = $1 AND cp.end_at > $2
		ORDER BY cp.start_at ASC
		LIMIT $3
	`, channelID, from, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProgramBlock
	for rows.Next() {
		var b ProgramBlock
		var vid *uuid.UUID
		var path, src *string
		if err := rows.Scan(&b.Seq, &b.Kind, &vid, &b.StartAt, &b.EndAt,
			&b.SourceOffsetMS, &b.SourceDurationMS, &path, &src); err != nil {
			return nil, err
		}
		if vid != nil {
			b.VideoID = *vid
		}
		if path != nil {
			b.Path = *path
		}
		if src != nil {
			b.SourceURL = *src
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Look reads channels.name, logo_path and mode_config->'look'.
func (r *PGRepo) Look(ctx context.Context, channelID uuid.UUID) (Look, error) {
	var name string
	var logo *string
	var raw []byte
	err := r.pool.QueryRow(ctx, `
		SELECT name, logo_path, mode_config->'look' FROM channels WHERE id = $1 AND enabled = true
	`, channelID).Scan(&name, &logo, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Look{}, ErrChannelNotFound
	}
	if err != nil {
		return Look{}, err
	}
	l := ParseLook(raw, deref(logo))
	l.Name = name
	return l, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (r *PGRepo) SetRuntime(ctx context.Context, rt Runtime) error {
	if rt.Host == "" {
		rt.Host = r.host
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO channel_runtime
			(channel_id, host, pid, state, viewer_count, started_at, last_segment_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (channel_id) DO UPDATE SET
			host = EXCLUDED.host,
			pid = EXCLUDED.pid,
			state = EXCLUDED.state,
			viewer_count = EXCLUDED.viewer_count,
			last_segment_at = EXCLUDED.last_segment_at
	`, rt.ChannelID, rt.Host, nullablePID(rt.PID), rt.State, rt.ViewerCount,
		nullableTime(rt.StartedAt), nullableTime(rt.LastSegmentAt))
	return err
}

func (r *PGRepo) ClearRuntime(ctx context.Context, channelID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM channel_runtime WHERE channel_id = $1`, channelID)
	return err
}

func nullablePID(pid int) any {
	if pid == 0 {
		return nil
	}
	return pid
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
