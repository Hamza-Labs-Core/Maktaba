package cluster

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"
)

const behind = "paused: home database at 0119, this machine needs 0124 — update the home"

func TestPauseReason(t *testing.T) {
	cases := []struct {
		name string
		h    HomeSchema
		want string
	}{
		{"home behind", HomeSchema{Applied: 119, Known: true}, behind},
		{"same", HomeSchema{Applied: 124, Known: true}, ""},
		{"home ahead, additive", HomeSchema{Applied: 130, Known: true}, ""},
		{"home ahead, compatible", HomeSchema{Applied: 130, Known: true, MinNode: 124, HasMinNode: true}, ""},
		{"home ahead, breaking", HomeSchema{Applied: 130, Known: true, MinNode: 127, HasMinNode: true},
			"paused: home database at 0130 needs this machine at 0127 or newer (it has 0124) — update this machine"},
		{"unreadable", HomeSchema{}, ""},
	}
	for _, c := range cases {
		if got := PauseReason(c.h, 124); got != c.want {
			t.Errorf("%s: PauseReason = %q, want %q", c.name, got, c.want)
		}
	}
}

// SchemaRev must name the highest migration slot this code ships.
func TestSchemaRevIsTheHighestMigration(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "shared", "db", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
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
		t.Fatalf("highest migration slot is %04d, SchemaRev is %04d: raise SchemaRev here and SCHEMA_REV in pipeline/src/maktaba_pipeline/cluster/schema.py", best, SchemaRev)
	}
}

func TestSchemaGuardWaitsForTheHome(t *testing.T) {
	applied := 119
	reads := 0
	g := &SchemaGuard{Required: 124, Interval: time.Millisecond, Read: func(context.Context) HomeSchema {
		reads++
		if reads == 3 {
			applied = 124
		}
		return HomeSchema{Applied: applied, Known: true}
	}}
	if got := g.Check(context.Background()); got != behind {
		t.Fatalf("Check = %q, want %q", got, behind)
	}
	if err := g.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if reads != 3 || g.Check(context.Background()) != "" {
		t.Fatalf("reads = %d, want 3 and a fitting schema", reads)
	}
}

func TestSchemaGuardWaitEndsWithTheContext(t *testing.T) {
	g := &SchemaGuard{Required: 124, Interval: time.Millisecond, Read: func(context.Context) HomeSchema {
		return HomeSchema{Applied: 1, Known: true}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := g.Wait(ctx); err == nil {
		t.Fatal("Wait returned nil while the home stayed behind")
	}
}

// TestSchemaGuardSharedTable: the verdicts a full node's API checks too (Epic 35 story
// 35.8 TC4, shared/testdata/cluster/schema_guard_cases.json).
func TestSchemaGuardSharedTable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "shared", "testdata", "cluster", "schema_guard_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Required int `json:"required"`
		Cases    []struct {
			Name string `json:"name"`
			Home struct {
				Applied    int  `json:"applied"`
				Known      bool `json:"known"`
				MinNode    int  `json:"min_node"`
				HasMinNode bool `json:"has_min_node"`
			} `json:"home"`
			Reason string `json:"reason"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Cases {
		h := HomeSchema{Applied: c.Home.Applied, Known: c.Home.Known, MinNode: c.Home.MinNode, HasMinNode: c.Home.HasMinNode}
		if got := PauseReason(h, f.Required); got != c.Reason {
			t.Errorf("%s: PauseReason = %q, want %q", c.Name, got, c.Reason)
		}
	}
}
