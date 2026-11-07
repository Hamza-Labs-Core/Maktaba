package pluginmanifest

import (
	"encoding/json"
	"os"
	"testing"
)

// Story 54.08 T04: the shared rating table.
func TestRatingTable(t *testing.T) {
	raw, err := os.ReadFile("../testdata/ratings.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []struct {
			Capabilities []string `json:"capabilities"`
			ReviewLevel  string   `json:"review_level"`
			Rating       int      `json:"rating"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Cases {
		if got := Rating(c.Capabilities, c.ReviewLevel); got != c.Rating {
			t.Errorf("%v %s: %d, want %d", c.Capabilities, c.ReviewLevel, got, c.Rating)
		}
	}
}
