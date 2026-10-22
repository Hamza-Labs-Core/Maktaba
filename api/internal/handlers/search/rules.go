package search

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/rules"
)

// Rules is rules.search (pipeline search_settings.py, the schema's source).
// The defaults are kept equal to schema.json by a test.
type Rules struct {
	PassagesPerVideo int
	PageSizeGrid     int
	PageSizeList     int
	PassagesPageSize int
	MaxVideos        int
	SemanticTopK     int
	ResultMinutes    int
	RecentLimit      int
}

// DefaultRules are the built-in rules.
func DefaultRules() Rules {
	return Rules{
		PassagesPerVideo: 3,
		PageSizeGrid:     24,
		PageSizeList:     20,
		PassagesPageSize: 20,
		MaxVideos:        500,
		SemanticTopK:     200,
		ResultMinutes:    15,
		RecentLimit:      20,
	}
}

// resultTTL is how long a search's results are kept after their last use.
func (r Rules) resultTTL() time.Duration { return time.Duration(r.ResultMinutes) * time.Minute }

// pageSize is the videos per page of a view ("grid" or anything else: list).
func (r Rules) pageSize(view string) int {
	if view == "grid" {
		return r.PageSizeGrid
	}
	return r.PageSizeList
}

// rules reads rules.search: the schema's defaults with the stored overrides that
// are in range (the rules handler checks them); no database, no table or a
// failed read keeps the defaults.
func (h *Handler) rules(ctx context.Context) Rules {
	cfg := DefaultRules()
	if h.DB == nil {
		return cfg
	}
	m, err := rules.Effective(ctx, h.DB, "search")
	if err != nil {
		return cfg
	}
	applyRuleInts(m, map[string]*int{
		"passages_per_video": &cfg.PassagesPerVideo,
		"page_size_grid":     &cfg.PageSizeGrid,
		"page_size_list":     &cfg.PageSizeList,
		"passages_page_size": &cfg.PassagesPageSize,
		"max_videos":         &cfg.MaxVideos,
		"semantic_top_k":     &cfg.SemanticTopK,
		"result_minutes":     &cfg.ResultMinutes,
		"recent_limit":       &cfg.RecentLimit,
	})
	return cfg
}

func applyRuleInts(m map[string]any, dst map[string]*int) {
	for key, p := range dst {
		switch v := m[key].(type) {
		case json.Number:
			if i, err := v.Int64(); err == nil && i > 0 {
				*p = int(i)
			} else if f, err := v.Float64(); err == nil && f >= 1 {
				*p = int(f)
			}
		case float64:
			if v >= 1 {
				*p = int(v)
			}
		case int:
			if v > 0 {
				*p = v
			}
		}
	}
}
