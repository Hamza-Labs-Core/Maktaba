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
	// HostID, when set, scopes ClearRuntime to this host's row.
	HostID string

	mu            sync.Mutex
	hasSourceURL  bool
	checkedSrcURL time.Time

	hasRTStats     bool
	checkedRTStats time.Time
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
		       cp.source_offset, cp.source_duration, v.path, `+srcCol+`, cp.title_snapshot
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
		var snap []byte
		if err := rows.Scan(&b.Seq, &b.Kind, &vid, &b.StartAt, &b.EndAt,
			&b.SourceOffsetMS, &b.SourceDurationMS, &path, &src, &snap); err != nil {
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
		b.Info = ParseBlockInfo(snap)
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
	if r.runtimeStatsColumns(ctx) {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO channel_runtime
				(channel_id, host, pid, state, viewer_count, started_at, last_segment_at,
				 host_id, stream_base_url, encoder, restarts, nvenc_fallbacks, heartbeat_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12, now())
			ON CONFLICT (channel_id) DO UPDATE SET
				host = EXCLUDED.host,
				pid = EXCLUDED.pid,
				state = EXCLUDED.state,
				viewer_count = EXCLUDED.viewer_count,
				started_at = CASE WHEN channel_runtime.host_id IS DISTINCT FROM EXCLUDED.host_id
				                  THEN EXCLUDED.started_at ELSE channel_runtime.started_at END,
				last_segment_at = EXCLUDED.last_segment_at,
				host_id = EXCLUDED.host_id,
				stream_base_url = EXCLUDED.stream_base_url,
				encoder = EXCLUDED.encoder,
				restarts = EXCLUDED.restarts,
				nvenc_fallbacks = EXCLUDED.nvenc_fallbacks,
				heartbeat_at = now()
		`, rt.ChannelID, rt.Host, nullablePID(rt.PID), rt.State, rt.ViewerCount,
			nullableTime(rt.StartedAt), nullableTime(rt.LastSegmentAt),
			nullableString(rt.HostID), nullableString(rt.StreamBaseURL),
			nullableString(rt.Encoder), rt.Restarts, rt.NVENCFallbacks)
		return err
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

// runtimeStatsColumns reports whether slot 0117's channel_runtime columns
// exist (a database not yet migrated gets the old row).
func (r *PGRepo) runtimeStatsColumns(ctx context.Context) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hasRTStats || (!r.checkedRTStats.IsZero() && time.Since(r.checkedRTStats) < sourceURLRecheck) {
		return r.hasRTStats
	}
	var ok bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'channel_runtime' AND column_name = 'heartbeat_at'
			  AND table_schema = ANY (current_schemas(false))
		)`).Scan(&ok)
	if err == nil {
		r.hasRTStats = ok
		r.checkedRTStats = time.Now()
	}
	return r.hasRTStats
}

// RuntimeOf reads a channel's channel_runtime row and its heartbeat.
func (r *PGRepo) RuntimeOf(ctx context.Context, channelID uuid.UUID) (Runtime, time.Time, bool, error) {
	if !r.runtimeStatsColumns(ctx) {
		return Runtime{}, time.Time{}, false, nil
	}
	var rt Runtime
	var hostID, base *string
	var hb *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT host, host_id, stream_base_url, state, heartbeat_at
		FROM channel_runtime WHERE channel_id = $1`, channelID).Scan(&rt.Host, &hostID, &base, &rt.State, &hb)
	if errors.Is(err, pgx.ErrNoRows) {
		return Runtime{}, time.Time{}, false, nil
	}
	if err != nil {
		return Runtime{}, time.Time{}, false, err
	}
	rt.ChannelID, rt.HostID, rt.StreamBaseURL = channelID, deref(hostID), deref(base)
	var at time.Time
	if hb != nil {
		at = *hb
	}
	return rt, at, true, nil
}

// SetHostStatus upserts this host's streaming_hosts row (slot 0117).
func (r *PGRepo) SetHostStatus(ctx context.Context, st HostStatus) error {
	if !r.runtimeStatsColumns(ctx) || st.Identity.ID == "" {
		return nil
	}
	role := st.Identity.Role
	if role != RoleNode {
		role = RoleHome
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO streaming_hosts
			(host_id, host, role, stream_base_url, encoder, max_active, active_channels, nvenc_free, vt_free, heartbeat_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, now())
		ON CONFLICT (host_id) DO UPDATE SET
			host = EXCLUDED.host, role = EXCLUDED.role, stream_base_url = EXCLUDED.stream_base_url,
			encoder = EXCLUDED.encoder, max_active = EXCLUDED.max_active,
			active_channels = EXCLUDED.active_channels, nvenc_free = EXCLUDED.nvenc_free,
			vt_free = EXCLUDED.vt_free, heartbeat_at = now()
	`, st.Identity.ID, st.Identity.Host, role, nullableString(st.Identity.StreamBaseURL),
		nullableString(st.Encoder), st.MaxActive, st.ActiveChannels, st.NVENCFree, st.VTFree)
	return err
}

// ClearHostRuntime deletes every channel_runtime row of this host (boot):
// rows with its host id, and rows from before slot 0117 under its name.
func (r *PGRepo) ClearHostRuntime(ctx context.Context, hostID, host string) (int64, error) {
	if host == "" {
		host = r.host
	}
	q := `DELETE FROM channel_runtime WHERE host = $1`
	args := []any{host}
	if r.runtimeStatsColumns(ctx) {
		q = `DELETE FROM channel_runtime WHERE host_id = $2 OR (host_id IS NULL AND host = $1)`
		args = append(args, hostID)
	}
	tag, err := r.pool.Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *PGRepo) ClearRuntime(ctx context.Context, channelID uuid.UUID) error {
	if r.HostID != "" && r.runtimeStatsColumns(ctx) {
		// only this host's row: a station stopped because its channel moved must not
		// delete the row the new host already wrote (one encode per channel)
		_, err := r.pool.Exec(ctx, `DELETE FROM channel_runtime WHERE channel_id = $1
			AND (host_id IS NULL OR host_id = $2)`, channelID, r.HostID)
		return err
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM channel_runtime WHERE channel_id = $1`, channelID)
	return err
}

func nullablePID(pid int) any {
	if pid == 0 {
		return nil
	}
	return pid
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
