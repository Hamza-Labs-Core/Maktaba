package search

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"sync"
	"time"

	grpcpipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
)

// notConfiguredBackoff is how long the semantic leg stays off after the
// pipeline answered that it has no semantic backend (its optional
// `search` extra is not installed): a deployment without it is not
// "degraded", it simply has no semantic search, so those requests are
// served FTS-only without the degraded flag and without a warning each.
const notConfiguredBackoff = time.Minute

// GRPCSemantic is the production SemanticClient: the pipeline's
// SemanticSearch RPC (embed with multilingual e5 + Chroma top-k) for the
// ranking, then transcript_segments for each hit's text, time span and
// video, with the request's filters applied in SQL.
//
// Hits whose segment no longer exists, belongs to an inactive transcript
// or a deleted video, or falls outside the filters are dropped, so a
// vector index that lags the database never surfaces stale passages.
type GRPCSemantic struct {
	Backend grpcpipeline.SemanticSearcher
	DB      *sql.DB
	NowFunc func() time.Time

	mu       sync.Mutex
	offUntil time.Time
}

// NewGRPCSemantic builds the SemanticClient over the pipeline client.
func NewGRPCSemantic(backend grpcpipeline.SemanticSearcher, db *sql.DB) *GRPCSemantic {
	return &GRPCSemantic{Backend: backend, DB: db}
}

func (g *GRPCSemantic) now() time.Time {
	if g.NowFunc != nil {
		return g.NowFunc()
	}
	return time.Now()
}

// Search implements SemanticClient.
func (g *GRPCSemantic) Search(ctx context.Context, q string, k int, f Filters) ([]Hit, error) {
	g.mu.Lock()
	off := g.now().Before(g.offUntil)
	g.mu.Unlock()
	if off {
		return []Hit{}, nil
	}
	raw, err := g.Backend.SemanticSearch(ctx, q, k, f.LibraryID)
	if err != nil {
		if strings.Contains(err.Error(), "backend not configured") {
			g.mu.Lock()
			g.offUntil = g.now().Add(notConfiguredBackoff)
			g.mu.Unlock()
			return []Hit{}, nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		return []Hit{}, nil
	}
	if g.DB == nil {
		out := make([]Hit, 0, len(raw))
		for _, r := range raw {
			out = append(out, Hit{
				SegmentID: r.SegmentID, VideoID: r.VideoID, StartSec: r.StartSec,
				EndSec: r.EndSec, Snippet: r.Text, Score: r.Score,
			})
		}
		return out, nil
	}
	rows, err := g.loadSegments(ctx, raw, f)
	if err != nil {
		return nil, err
	}
	out := make([]Hit, 0, len(raw))
	for _, r := range raw {
		row, ok := rows[r.SegmentID]
		if !ok {
			continue
		}
		row.Score = r.Score
		out = append(out, row)
	}
	return out, nil
}

// loadSegments reads the hits' segments (text, span, video) in one query,
// keeping only those that pass the filters.
func (g *GRPCSemantic) loadSegments(ctx context.Context, raw []grpcpipeline.SemanticHit, f Filters) (map[int64]Hit, error) {
	args := make([]any, 0, len(raw)+len(f.LibraryID)+len(f.Language))
	ph := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	ids := make([]string, len(raw))
	for i, r := range raw {
		ids[i] = ph(r.SegmentID)
	}
	where := []string{
		"s.id IN (" + strings.Join(ids, ",") + ")",
		"t.is_active",
		"v.deleted_at IS NULL",
	}
	if len(f.LibraryID) > 0 {
		libs := make([]string, len(f.LibraryID))
		for i, l := range f.LibraryID {
			libs[i] = ph(l)
		}
		where = append(where, "v.library_id IN ("+strings.Join(libs, ",")+")")
	}
	if len(f.Language) > 0 {
		langs := make([]string, len(f.Language))
		for i, l := range f.Language {
			langs[i] = ph(l)
		}
		where = append(where, "v.detected_language IN ("+strings.Join(langs, ",")+")")
	}
	query := `
		SELECT s.id, t.video_id, s.start_sec, s.end_sec, s.text
		FROM transcript_segments s
		JOIN transcripts t ON t.id = s.transcript_id
		JOIN videos v ON v.id = t.video_id
		WHERE ` + strings.Join(where, " AND ")
	rs, err := g.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := make(map[int64]Hit, len(raw))
	for rs.Next() {
		var h Hit
		if err := rs.Scan(&h.SegmentID, &h.VideoID, &h.StartSec, &h.EndSec, &h.Snippet); err != nil {
			return nil, err
		}
		out[h.SegmentID] = h
	}
	return out, rs.Err()
}

// compile-time check.
var _ SemanticClient = (*GRPCSemantic)(nil)
