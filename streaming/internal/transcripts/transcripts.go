// Package transcripts is the Streaming service's read side of the
// transcript_segments table — the source for the auto-generated VTT
// subtitle endpoint (Story 8.11 AC-1). Pipeline owns the write side
// (Epic 3); here we only page the active transcript's segments.
package transcripts

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/handlers"
)

// PGStreamer implements handlers.TranscriptStreamer against Postgres.
type PGStreamer struct {
	pool *pgxpool.Pool
}

// NewPGStreamer wraps an already-open pool.
func NewPGStreamer(pool *pgxpool.Pool) *PGStreamer {
	return &PGStreamer{pool: pool}
}

// rowScanner is the slice of pgx.Rows the segment mapper needs. The
// seam lets the unit suite drive mapSegments without a live Postgres.
type rowScanner interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

// streamSQL pages the *active* transcript's segments in seq order. Only
// the active transcript is served — a video may carry several
// historical transcripts (Story 3.5), but the player wants the current
// one. seq is the stable, gap-free ordering key the cue index derives
// from.
const streamSQL = `
SELECT ts.seq, ts.start_sec, ts.end_sec, COALESCE(ts.speaker, ''), ts.text
FROM transcript_segments ts
JOIN transcripts t ON t.id = ts.transcript_id
WHERE t.video_id = $1 AND t.is_active = true
ORDER BY ts.seq
LIMIT $2 OFFSET $3`

// Stream returns one page of segments for videoID. page is 0-based;
// an out-of-range page yields an empty slice (the handler's loop
// terminates on a short/empty page).
func (s *PGStreamer) Stream(ctx context.Context, videoID string, page, pageSize int) ([]handlers.TranscriptSegment, error) {
	id, err := uuid.Parse(videoID)
	if err != nil {
		return nil, fmt.Errorf("transcripts: bad video id %q: %w", videoID, err)
	}
	if pageSize <= 0 {
		return nil, nil
	}
	offset := page * pageSize
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, streamSQL, id, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("transcripts: query video %s: %w", id, err)
	}
	defer rows.Close()
	return mapSegments(rows)
}

// mapSegments drains a result set into TranscriptSegment values. The
// VTT renderer treats seq as the cue index, so we carry it through as
// Index verbatim.
func mapSegments(rows rowScanner) ([]handlers.TranscriptSegment, error) {
	var out []handlers.TranscriptSegment
	for rows.Next() {
		var seg handlers.TranscriptSegment
		if err := rows.Scan(&seg.Index, &seg.StartSec, &seg.EndSec, &seg.Speaker, &seg.Text); err != nil {
			return nil, fmt.Errorf("transcripts: scan: %w", err)
		}
		out = append(out, seg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("transcripts: iterate: %w", err)
	}
	return out, nil
}

// compile-time assertions.
var (
	_ handlers.TranscriptStreamer = (*PGStreamer)(nil)
	_ rowScanner                  = (pgx.Rows)(nil)
)
