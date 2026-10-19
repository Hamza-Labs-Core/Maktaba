package schemaguard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// TestSchemaGuardTable checks the verdicts against the fixtures the streaming node and
// the pipeline share (story 35.8 TC4).
func TestSchemaGuardTable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "shared", "testdata", "cluster", "schema_guard_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Required int `json:"required"`
		Cases    []struct {
			Name   string     `json:"name"`
			Home   HomeSchema `json:"home"`
			Reason string     `json:"reason"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		if got := PauseReason(c.Home, f.Required); got != c.Reason {
			t.Errorf("%s: PauseReason = %q, want %q", c.Name, got, c.Reason)
		}
	}
}

// SchemaRev must name the highest migration slot this code ships.
func TestSchemaRevIsTheHighestMigration(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "..", "shared", "db", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)
	best := 0
	for _, e := range entries {
		if m := re.FindStringSubmatch(e.Name()); m != nil {
			n, _ := strconv.Atoi(m[1])
			best = max(best, n)
		}
	}
	if best != SchemaRev {
		t.Fatalf("highest migration slot is %04d, SchemaRev is %04d: raise it here, in streaming/internal/cluster/schema.go and in pipeline/src/maktaba_pipeline/cluster/schema.py", best, SchemaRev)
	}
}

func TestGuardKeepsTheVerdict(t *testing.T) {
	applied := 100
	g := &Guard{Required: 120, ReadFn: func(context.Context) HomeSchema { return HomeSchema{Applied: applied, Known: true} }}
	if g.Check(context.Background()) == "" || g.Paused() == "" {
		t.Fatal("a home behind must pause the node")
	}
	applied = 120
	if g.Check(context.Background()) != "" || g.Paused() != "" {
		t.Fatal("the home caught up: the node runs")
	}
	var nilGuard *Guard
	if nilGuard.Paused() != "" {
		t.Fatal("a nil guard never pauses")
	}
}
