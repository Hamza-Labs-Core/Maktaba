package compat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(append([]string{filepath.Dir(file), "..", "..", ".."}, parts...)...)
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"1.4.0", "1.4.0", 0, true},
		{"1.3.9", "1.4.0", -1, true},
		{"1.10.0", "1.9.0", 1, true},
		{"1.4", "1.4.0", 0, true},
		{"0.3.140", "0.3.99", 1, true},
		{"1.3.9-beta", "1.4.0", -1, true},
		{"1.4.0-rc1", "1.4.0", 0, true},
		{"v2", "1.9", 1, true},
		{"dev", "1.0", 0, false},
		{"1.0", "", 0, false},
		{"unknown", "unknown", 0, false},
	}
	for _, c := range cases {
		got, ok := CompareVersions(c.a, c.b)
		if got != c.want || ok != c.ok {
			t.Errorf("CompareVersions(%q, %q) = %d, %v; want %d, %v", c.a, c.b, got, ok, c.want, c.ok)
		}
	}
}

func TestTheLevelsHold(t *testing.T) {
	if Level < 1 || Oldest < 1 || Oldest > Level {
		t.Fatalf("Level %d, Oldest %d: want 1 <= Oldest <= Level", Level, Oldest)
	}
}

type vectorFile struct {
	Cases []struct {
		Name    string          `json:"name"`
		Home    json.RawMessage `json:"home"`
		App     App             `json:"app"`
		Verdict Verdict         `json:"verdict"`
	} `json:"cases"`
}

// T04: the shared decision vectors (shared/compat/vectors.json), which every app's copy of
// Decide runs too.
func TestDecideVectors(t *testing.T) {
	raw, err := os.ReadFile(repoFile(t, "shared", "compat", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f vectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) < 10 {
		t.Fatalf("%d cases: the vectors lost their cases", len(f.Cases))
	}
	seen := map[Verdict]bool{}
	for _, c := range f.Cases {
		var home Home
		if err := json.Unmarshal(c.Home, &home); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if got := Decide(home, c.App); got != c.Verdict {
			t.Errorf("%s: Decide = %s, want %s", c.Name, got, c.Verdict)
		}
		seen[c.Verdict] = true
	}
	for _, v := range []Verdict{Carry, UpdateHome, UpdateApp} {
		if !seen[v] {
			t.Errorf("no vector ends in %s", v)
		}
	}
}

func TestFloorBelow(t *testing.T) {
	f := Floor{"tv-web/webos": "1.4.0"}
	if floor, below := f.Below("tv-web", "webos", "1.3.9"); !below || floor != "1.4.0" {
		t.Fatalf("1.3.9 = %q %v, want below 1.4.0", floor, below)
	}
	if _, below := f.Below("tv-web", "webos", "1.4.0"); below {
		t.Fatal("1.4.0 is the floor, not below it")
	}
	if _, below := f.Below("tv-web", "tizen", "0.1"); below {
		t.Fatal("tizen has no floor")
	}
	if _, below := f.Below("tv-web", "webos", "unknown"); below {
		t.Fatal("an unreadable version is never below")
	}
	var none Floor
	if _, below := none.Below("tv-web", "webos", "0.1"); below {
		t.Fatal("a nil floor refuses nothing")
	}
}
