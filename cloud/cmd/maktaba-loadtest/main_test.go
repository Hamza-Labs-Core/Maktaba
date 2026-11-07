package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestBitrates(t *testing.T) {
	for in, want := range map[string]int{"5M": 5_000_000, "2.5M": 2_500_000, "8M": 8_000_000, "640k": 640_000, "1000": 1000} {
		if got, err := parseBits(in); err != nil || got != want {
			t.Errorf("%s: %d %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "fast", "-5M", "0"} {
		if _, err := parseBits(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// The base runs in the same job, so a missing baseline fails: a gate that measured nothing must
// not pass. A base and a current 25 % worse fail; within 20 % passes.
func TestCompareFailsClosed(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cur := write("cur.json", `{"method":"cgroup-median-v1","cpu_seconds_per_mbit":0.0005,"mem_bytes_per_tunnel":100}`)
	if err := compare([]string{"--baseline", filepath.Join(dir, "missing.json"), "--current", cur}, io.Discard); err == nil {
		t.Fatal("no baseline passed")
	}
	base := write("base.json", `{"method":"cgroup-median-v1","cpu_seconds_per_mbit":0.0004,"mem_bytes_per_tunnel":100}`)
	if err := compare([]string{"--baseline", base, "--current", cur}, io.Discard); err == nil {
		t.Fatal("25 % worse passed")
	}
	ok := write("ok.json", `{"method":"cgroup-median-v1","cpu_seconds_per_mbit":0.00044,"mem_bytes_per_tunnel":110}`)
	if err := compare([]string{"--baseline", base, "--current", ok}, io.Discard); err != nil {
		t.Fatalf("10 %% worse failed: %v", err)
	}
}
