package pipeline

import (
	"context"
	"testing"
)

func TestRealClient_SemanticSearch(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"SemanticSearch": func(req map[string]any) map[string]any {
			if req["q"] != "صلاة" || req["k"] != float64(7) {
				t.Errorf("req=%v", req)
			}
			libs, _ := req["library_ids"].([]any)
			if len(libs) != 1 || libs[0] != "lib-a" {
				t.Errorf("library_ids=%v", req["library_ids"])
			}
			return map[string]any{"hits": []any{
				map[string]any{
					"segment_id": float64(42), "video_id": "v1",
					"start_sec": 1.5, "end_sec": 4.0, "score": 0.91, "text": "صلاة الفجر",
				},
				map[string]any{"video_id": "v2"}, // no segment id: dropped
				"junk",
			}}
		},
	})
	hits, err := c.SemanticSearch(context.Background(), "صلاة", 7, []string{"lib-a"})
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	want := SemanticHit{SegmentID: 42, VideoID: "v1", StartSec: 1.5, EndSec: 4, Score: 0.91, Text: "صلاة الفجر"}
	if len(hits) != 1 || hits[0] != want {
		t.Fatalf("hits=%+v", hits)
	}
}

func TestRealClient_SemanticSearch_NilLibrariesSendsEmptyList(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"SemanticSearch": func(req map[string]any) map[string]any {
			if libs, ok := req["library_ids"].([]any); !ok || len(libs) != 0 {
				t.Errorf("library_ids=%#v want []", req["library_ids"])
			}
			return map[string]any{"hits": []any{}}
		},
	})
	hits, err := c.SemanticSearch(context.Background(), "x", 5, nil)
	if err != nil || len(hits) != 0 {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
}

func TestRealClient_SemanticSearch_Errors(t *testing.T) {
	c := newFakePipelineClient(t, map[string]func(map[string]any) map[string]any{
		"SemanticSearch": func(req map[string]any) map[string]any {
			if req["q"] == "bad" {
				return map[string]any{"error": "semantic search backend not configured"}
			}
			return map[string]any{"nothing": true}
		},
	})
	if _, err := c.SemanticSearch(context.Background(), "bad", 5, nil); err == nil {
		t.Fatal("in-band error must surface")
	}
	if _, err := c.SemanticSearch(context.Background(), "x", 5, nil); err == nil {
		t.Fatal("a response without hits must be an error")
	}
	unconnected := NewRealClient(DefaultConfig()).(SemanticSearcher)
	if _, err := unconnected.SemanticSearch(context.Background(), "x", 5, nil); err == nil {
		t.Fatal("an unconnected client must fail, not hang")
	}
}
