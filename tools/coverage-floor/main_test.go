package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseFloors(t *testing.T) {
	d := t.TempDir()
	p := writeFile(t, d, "floors.yaml", `# header comment

api:        33    # measured 33.6
streaming: 60
pipeline:   48%
`)
	got, err := parseFloors(p)
	if err != nil {
		t.Fatalf("parseFloors: %v", err)
	}
	want := map[string]float64{"api": 33, "streaming": 60, "pipeline": 48}
	if len(got) != len(want) {
		t.Fatalf("want %d entries, got %d: %+v", len(want), len(got), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("floor %q = %v, want %v", k, got[k], v)
		}
	}
}

func TestParseFloorsRejectsMalformed(t *testing.T) {
	d := t.TempDir()
	p := writeFile(t, d, "bad.yaml", "api 33\n")
	if _, err := parseFloors(p); err == nil {
		t.Fatal("expected error for line missing ':'")
	}
}

func TestProfileCoverage(t *testing.T) {
	d := t.TempDir()
	// 10 statements total, 7 covered (count > 0) => 70%.
	prof := writeFile(t, d, "c.out", `mode: set
github.com/x/a.go:1.1,3.2 4 1
github.com/x/a.go:5.1,6.2 3 1
github.com/x/b.go:1.1,2.2 3 0
`)
	pct, err := profileCoverage(prof)
	if err != nil {
		t.Fatalf("profileCoverage: %v", err)
	}
	if pct < 69.99 || pct > 70.01 {
		t.Fatalf("want ~70%%, got %.4f", pct)
	}
}

func TestProfileCoverageEmptyIsError(t *testing.T) {
	d := t.TempDir()
	prof := writeFile(t, d, "empty.out", "mode: set\n")
	if _, err := profileCoverage(prof); err == nil {
		t.Fatal("an empty profile (zero statements) must be an error, not a silent pass")
	}
}

func TestSplitKV(t *testing.T) {
	if k, v, ok := splitKV("api=/tmp/c.out"); !ok || k != "api" || v != "/tmp/c.out" {
		t.Fatalf("splitKV bad result: %q %q %v", k, v, ok)
	}
	if _, _, ok := splitKV("noequals"); ok {
		t.Fatal("splitKV should reject input without '='")
	}
	if _, _, ok := splitKV("=val"); ok {
		t.Fatal("splitKV should reject empty key")
	}
}
