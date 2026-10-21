package probe

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGBackend is the production Backend: it reads the videos→media_info
// join Pipeline writes (Story 8.15). Streaming never probes a file
// itself (AC-3) — a video with no media_info row surfaces as
// ErrNotProbed so the caller returns 412 rather than blocking on a
// probe the read path is not allowed to run.
//
// The pool is owned by the caller (main.go) so its lifetime matches
// the process; Close releases it on shutdown.
type PGBackend struct {
	pool *pgxpool.Pool
}

// pgQuerier is the slice of *pgxpool.Pool the backend needs. Narrowing
// to this interface lets the test suite inject a fake without a live
// Postgres (mirrors the api packages' DBConn seams).
type pgQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// NewPGBackend wraps an already-open pgx pool.
func NewPGBackend(pool *pgxpool.Pool) *PGBackend {
	return &PGBackend{pool: pool}
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
	return &PGBackend{pool: pool}, pool, nil
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
  (mi.video_id IS NOT NULL) AS probed
FROM videos v
LEFT JOIN media_info mi ON mi.video_id = v.id
LEFT JOIN LATERAL (
  SELECT codec, channels
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
	return lookupOn(ctx, b.pool, videoID)
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
