package pipeline

import (
	"context"
	"fmt"
)

// methodSemanticSearch must match the "SemanticSearch" handler key in
// pipeline/src/maktaba_pipeline/grpc_server.py.
const methodSemanticSearch = "/" + pipelineService + "/SemanticSearch"

// SemanticHit is one transcript segment returned by Pipeline.SemanticSearch:
// the segment's identity and time span plus its cosine similarity to the
// query (larger is closer). Text is the segment text as indexed; callers
// that need the current text load it from transcript_segments.
type SemanticHit struct {
	SegmentID int64
	VideoID   string
	StartSec  float64
	EndSec    float64
	Score     float64
	Text      string
}

// SemanticSearcher is the semantic-search RPC. It is a separate interface
// from Client so existing Client fakes need not implement it; the real
// client returned by NewRealClient satisfies both.
type SemanticSearcher interface {
	// SemanticSearch embeds q (e5 "query: " prefix) and returns the k
	// nearest indexed segments, only from libraryIDs and of languages
	// (the video's detected language) when non-empty: the filters apply
	// before the top k is taken, so a filtered search is not cut short.
	SemanticSearch(ctx context.Context, q string, k int, libraryIDs, languages []string) ([]SemanticHit, error)
}

// SemanticSearch implements SemanticSearcher over the JSON-on-gRPC wire.
// It shares Embed's per-call timeout; callers usually pass a tighter
// context deadline (the search handler's semantic budget).
func (c *realClient) SemanticSearch(ctx context.Context, q string, k int, libraryIDs, languages []string) ([]SemanticHit, error) {
	if c.cfg.EmbedTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.EmbedTimeout)
		defer cancel()
	}
	libs := libraryIDs
	if libs == nil {
		libs = []string{}
	}
	var resp map[string]any
	langs := languages
	if langs == nil {
		langs = []string{}
	}
	req := map[string]any{"q": q, "k": k, "library_ids": libs, "languages": langs}
	if err := c.invoke(ctx, methodSemanticSearch, req, &resp); err != nil {
		return nil, err
	}
	raw, ok := resp["hits"].([]any)
	if !ok {
		if _, present := resp["hits"]; present && resp["hits"] == nil {
			return []SemanticHit{}, nil
		}
		return nil, fmt.Errorf("pipeline SemanticSearch: malformed response (missing hits)")
	}
	out := make([]SemanticHit, 0, len(raw))
	for _, e := range raw {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		seg, ok := m["segment_id"].(float64)
		if !ok || asString(m["video_id"]) == "" {
			continue // a hit without its identity cannot be shown
		}
		out = append(out, SemanticHit{
			SegmentID: int64(seg),
			VideoID:   asString(m["video_id"]),
			StartSec:  asFloat(m["start_sec"]),
			EndSec:    asFloat(m["end_sec"]),
			Score:     asFloat(m["score"]),
			Text:      asString(m["text"]),
		})
	}
	return out, nil
}

// compile-time check: the real client is a SemanticSearcher.
var _ SemanticSearcher = (*realClient)(nil)
