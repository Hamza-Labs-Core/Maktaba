package libraries

import (
	"encoding/json"
	"testing"
)

func TestDecodeIntMap_HandlesEmpty(t *testing.T) {
	if got := decodeIntMap(nil); len(got) != 0 {
		t.Errorf("nil should decode to empty: %v", got)
	}
	if got := decodeIntMap([]byte(`{}`)); len(got) != 0 {
		t.Errorf("empty obj should decode to empty: %v", got)
	}
}

func TestDecodeIntMap_DecodesIntegers(t *testing.T) {
	got := decodeIntMap([]byte(`{"ready":3,"failed":1}`))
	if got["ready"] != 3 || got["failed"] != 1 {
		t.Errorf("unexpected: %v", got)
	}
}

func TestDecodeIntMap_RejectsBadJSON(t *testing.T) {
	got := decodeIntMap([]byte(`not-json`))
	// Should return empty map, not panic.
	if got == nil {
		t.Error("expected non-nil empty map")
	}
}

func TestNullableJSON_PassesThroughNonEmpty(t *testing.T) {
	v := nullableJSON([]byte(`{"a":1}`))
	s, ok := v.(string)
	if !ok || s != `{"a":1}` {
		t.Errorf("expected string passthrough, got %T %v", v, v)
	}
}

func TestNullableJSON_NilForEmpty(t *testing.T) {
	if v := nullableJSON(nil); v != nil {
		t.Errorf("expected nil, got %v", v)
	}
	if v := nullableJSON([]byte{}); v != nil {
		t.Errorf("expected nil for empty slice, got %v", v)
	}
}

func TestCachedStatsResponse_RoundTripsJSON(t *testing.T) {
	pct := 75.5
	r := &CachedStatsResponse{
		TotalVideos:      10,
		TotalDurationSec: 3600,
		ByState:          map[string]int{"ready": 7, "failed": 3},
		ByLanguage:       map[string]int{"ar": 5, "en": 5},
		ByContentType:    map[string]int{"lecture": 8},
		Jobs:             map[string]int{"pending": 2},
		ProcessedPct:     &pct,
	}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back CachedStatsResponse
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back.TotalVideos != 10 || back.ByState["ready"] != 7 {
		t.Errorf("round-trip lost data: %+v", back)
	}
	if back.ProcessedPct == nil || *back.ProcessedPct != pct {
		t.Errorf("processed_pct lost: %+v", back.ProcessedPct)
	}
}
