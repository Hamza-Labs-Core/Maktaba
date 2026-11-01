package probe

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGBackend is the production Backend: it reads the videos→media_info
// join Pipeline writes (Story 8.15). Streaming never probes a file
// itself (AC-3): for a video with no media_info row it puts the
// Pipeline's probe job at the front of the queue and waits a short
// while for the row (ProbeWait), so pressing play on a video nobody
// probed yet plays instead of failing. Past the wait it surfaces
// ErrNotProbed and the caller returns 412.
//
// The pool is owned by the caller (main.go) so its lifetime matches
// the process; Close releases it on shutdown.
type PGBackend struct {
	pool *pgxpool.Pool
	// ProbeWait bounds the wait for a probe requested on play; 0 = do not ask.
	ProbeWait time.Duration
}

// DefaultProbeWait is how long a play waits for its video's probe: a probe reads
// the file's header, about a second once a worker takes it.
const DefaultProbeWait = 15 * time.Second

// probePollEvery is how often the wait looks for the media_info row.
const probePollEvery = 250 * time.Millisecond

// pgQuerier is the slice of *pgxpool.Pool the backend needs. Narrowing
// to this interface lets the test suite inject a fake without a live
// Postgres (mirrors the api packages' DBConn seams).
type pgQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// pgExecQuerier adds Exec, for queueing the probe.
type pgExecQuerier interface {
	pgQuerier
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// NewPGBackend wraps an already-open pgx pool.
func NewPGBackend(pool *pgxpool.Pool) *PGBackend {
	return &PGBackend{pool: pool, ProbeWait: DefaultProbeWait}
}

// ConnectPG dials Postgres with the given DSN and returns a backend
// plus the underlying pool (so the caller can Close it on shutdown).
// The pool is pinged once so a misconfigured DATABASE_URL fails fast
// at boot rather than on the first stream request.
func ConnectPG(ctx context.Context, dsn string) (*PGBackend, *pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("probe: pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("probe: ping: %w", err)
	}
	return &PGBackend{pool: pool, ProbeWait: DefaultProbeWait}, pool, nil
}

// Close releases the pool.
func (b *PGBackend) Close() {
	if b.pool != nil {
		b.pool.Close()
	}
}

// lookupSQL joins the three tables Streaming's Row needs:
//
//   - videos        — library_id, content_hash, path, size, mtime, duration
//   - media_info    — container, video_codec, width, height, bitrate
//     (its presence is what "probed" means)
//   - audio_tracks  — the default/first track's codec + channels, which
//     the capability matrix reasons over for direct-play vs transcode
//   - has_audio     — any audio_tracks row, or an audio stream in raw_ffprobe:
//     a track row with no codec is still audio (Row.NoAudio)
//   - media_info.raw_ffprobe — the first video stream (not cover art): its
//     colour and pixel format, for the transcode's tone-mapping
//
// HDR and MIME have no columns in the current schema; the matrix
// treats an empty HDR as SDR and direct-play derives Content-Type from
// the container, so leaving them empty is correct rather than a stub.
const lookupSQL = `
SELECT
  v.id,
  v.library_id,
  v.content_hash,
  v.path,
  COALESCE(mi.container, ''),
  COALESCE(mi.video_codec, ''),
  COALESCE(at.codec, ''),
  COALESCE(mi.height, 0),
  COALESCE(mi.width, 0),
  COALESCE(v.duration_sec, 0),
  COALESCE(mi.bitrate_kbps, 0),
  COALESCE(at.channels, 0),
  v.size_bytes,
  v.mtime,
  (mi.video_id IS NOT NULL) AS probed,
  COALESCE(vs.s->>'color_transfer', ''),
  COALESCE(vs.s->>'color_space', ''),
  COALESCE(vs.s->>'color_primaries', ''),
  COALESCE(vs.s->>'color_range', ''),
  COALESCE(vs.s->>'pix_fmt', ''),
  (at.present IS NOT NULL OR EXISTS (
     SELECT 1
     FROM jsonb_array_elements(CASE WHEN jsonb_typeof(mi.raw_ffprobe->'streams') = 'array'
                                    THEN mi.raw_ffprobe->'streams' ELSE '[]'::jsonb END) AS au(stream)
     WHERE au.stream->>'codec_type' = 'audio'
  )) AS has_audio
FROM videos v
LEFT JOIN media_info mi ON mi.video_id = v.id
LEFT JOIN LATERAL (
  SELECT s
  FROM jsonb_array_elements(CASE WHEN jsonb_typeof(mi.raw_ffprobe->'streams') = 'array'
                                 THEN mi.raw_ffprobe->'streams' ELSE '[]'::jsonb END) s
  WHERE s->>'codec_type' = 'video'
    AND COALESCE(s->'disposition'->>'attached_pic', '0') <> '1'
  LIMIT 1
) vs ON true
LEFT JOIN LATERAL (
  SELECT codec, channels, true AS present
  FROM audio_tracks
  WHERE video_id = v.id
  ORDER BY is_default DESC, track_index ASC
  LIMIT 1
) at ON true
WHERE v.id = $1`

// Lookup implements Backend. Returns ErrNotFound when no videos row
// exists and ErrNotProbed when the video exists but Pipeline has not
// written its media_info row yet.
func (b *PGBackend) Lookup(ctx context.Context, videoID uuid.UUID) (*Row, error) {
	row, err := lookupOn(ctx, b.pool, videoID)
	if !errors.Is(err, ErrNotProbed) || b.ProbeWait <= 0 {
		return row, err
	}
	return probeOnPlay(ctx, b.pool, videoID, b.ProbeWait, probePollEvery)
}

// bumpProbeSQL moves the video's open probe job to the front of the queue (priority 0,
// claimable now); insertProbeSQL queues one when there is none. The partial unique
// index on open jobs turns a racing insert into a unique violation, which is fine:
// some job is queued either way.
const bumpProbeSQL = `
UPDATE processing_jobs SET priority = 0, not_before = NULL, updated_at = now()
 WHERE subject_kind = 'video' AND subject_id = $1 AND stage = 'probe'
   AND state IN ('pending', 'claimed', 'running', 'resuming', 'paused')`

const insertProbeSQL = `
INSERT INTO processing_jobs (subject_kind, subject_id, video_id, library_id, stage, state,
                             priority, max_attempts, reasons)
SELECT 'video', v.id, v.id, v.library_id, 'probe', 'pending', 0, 3,
       '[{"why": "played before it was probed", "by": "system", "from_stage": "streaming"}]'::jsonb
  FROM videos v WHERE v.id = $1`

// probeOnPlay queues the video's probe at the front and waits up to wait for its
// media_info row. ErrNotProbed when the wait runs out (no worker free, the probe
// failed) or the queueing itself fails.
func probeOnPlay(ctx context.Context, q pgExecQuerier, videoID uuid.UUID, wait, every time.Duration) (*Row, error) {
	tag, err := q.Exec(ctx, bumpProbeSQL, videoID)
	if err == nil && tag.RowsAffected() == 0 {
		_, err = q.Exec(ctx, insertProbeSQL, videoID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			err = nil
		}
	}
	if err != nil {
		return nil, ErrNotProbed
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ErrNotProbed
		case <-tick.C:
		}
		row, err := lookupOn(ctx, q, videoID)
		if ctx.Err() != nil {
			return nil, ErrNotProbed
		}
		if !errors.Is(err, ErrNotProbed) {
			return row, err
		}
	}
}

// lookupOn is the queryable-agnostic core so the test suite can drive
// it against a fake pgx.Row source.
func lookupOn(ctx context.Context, q pgQuerier, videoID uuid.UUID) (*Row, error) {
	row := &Row{VideoID: videoID}
	err := q.QueryRow(ctx, lookupSQL, videoID).Scan(
		&row.VideoID,
		&row.LibraryID,
		&row.ContentHash,
		&row.Path,
		&row.Container,
		&row.VideoCodec,
		&row.AudioCodec,
		&row.Height,
		&row.Width,
		&row.DurationSec,
		&row.BitrateKbps,
		&row.AudioChannels,
		&row.Size,
		&row.ModTime,
		&row.Probed,
		&row.ColorTransfer,
		&row.ColorMatrix,
		&row.ColorPrimaries,
		&row.ColorRange,
		&row.PixFmt,
		&row.HasAudio,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("probe: lookup video %s: %w", videoID, err)
	}
	if !row.Probed {
		return nil, ErrNotProbed
	}
	return row, nil
}
