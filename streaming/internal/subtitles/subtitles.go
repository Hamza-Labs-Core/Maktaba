// Package subtitles is the Streaming service's read side of the subtitles kept in the
// database (Epic 59 story 59.03): subtitle_files rows whose content the pipeline stored
// because the video's library is read-only for Maktaba. They replicate with the table, so a
// node's replica and a node taking over the home serve them too.
package subtitles

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/handlers"
)

// PGStore implements handlers.StoredSubtitles against Postgres.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore wraps an already-open pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const langsSQL = `SELECT DISTINCT language FROM subtitle_files
WHERE video_id = $1::uuid AND content IS NOT NULL AND deleted_at IS NULL ORDER BY language`

// VTT first: it is served as it is; an SRT is converted.
const contentSQL = `SELECT format, content FROM subtitle_files
WHERE video_id = $1::uuid AND language = $2 AND content IS NOT NULL AND deleted_at IS NULL
ORDER BY (format = 'vtt') DESC, created_at DESC LIMIT 1`

// Langs implements handlers.StoredSubtitles.
func (s *PGStore) Langs(ctx context.Context, videoID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, langsSQL, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// VTT implements handlers.StoredSubtitles.
func (s *PGStore) VTT(ctx context.Context, videoID, lang string) ([]byte, error) {
	var format, content string
	err := s.pool.QueryRow(ctx, contentSQL, videoID, lang).Scan(&format, &content)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, handlers.ErrSidecarNotFound
	}
	if err != nil {
		return nil, err
	}
	return AsVTT(format, content)
}

// AsVTT is a stored subtitle as WebVTT: a VTT as it is, an SRT converted (cue text
// HTML-escaped, as a side-car's is).
func AsVTT(format, content string) ([]byte, error) {
	if format == "vtt" {
		return []byte(content), nil
	}
	return handlers.SrtToVtt(strings.NewReader(content))
}
