package logbench

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// The corpus is the same for a seed, and its mix is the story's: mostly info, some warn and
// error, Arabic names, request ids shared by a few lines.
func TestCorpusIsSeededAndMixed(t *testing.T) {
	end := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	a, b := Corpus(7, 2000, end, time.Hour), Corpus(7, 2000, end, time.Hour)
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			t.Fatalf("line %d differs for one seed", i)
		}
	}
	levels := map[string]int{}
	ids := map[string]int{}
	arabic := 0
	for _, l := range a {
		var m struct {
			Level     string `json:"level"`
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(l, &m); err != nil {
			t.Fatal(err)
		}
		levels[m.Level]++
		ids[m.RequestID]++
		if bytes.ContainsAny(l, "مكتبة") {
			arabic++
		}
	}
	if levels["info"] < 1400 || levels["warn"] < 200 || levels["error"] < 40 || arabic == 0 || len(ids) > 600 {
		t.Fatalf("mix: %v, %d request ids, %d Arabic", levels, len(ids), arabic)
	}
	if f := Calibrate(); f < 1 || f > 4 {
		t.Fatalf("calibration factor %v", f)
	}
}
