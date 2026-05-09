package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRunJSONUnderBudget(t *testing.T) {
	in := strings.NewReader(`
{"Time":"t","Action":"run","Package":"p1","Test":"TestA"}
{"Time":"t","Action":"pass","Package":"p1","Test":"TestA","Elapsed":0.01}
{"Time":"t","Action":"pass","Package":"p1","Elapsed":0.02}
`)
	cfg := &config{
		mode:             "json",
		perPackageBudget: 30 * time.Second,
		perTestSoftCap:   100 * time.Millisecond,
		tier:             "unit",
	}
	var out bytes.Buffer
	got := runJSON(cfg, in, &out)
	if got != 0 {
		t.Fatalf("expected exit 0, got %d (out=%q)", got, out.String())
	}
	if !strings.Contains(out.String(), "1 packages") {
		t.Fatalf("expected package count in output, got %q", out.String())
	}
}

func TestRunJSONOverBudget(t *testing.T) {
	// Package elapsed is 5s, budget 1s — must report breach.
	in := strings.NewReader(`
{"Time":"t","Action":"pass","Package":"slow","Test":"TestX","Elapsed":5}
{"Time":"t","Action":"pass","Package":"slow","Elapsed":5}
`)
	cfg := &config{
		mode:             "json",
		perPackageBudget: 1 * time.Second,
		perTestSoftCap:   100 * time.Millisecond,
		tier:             "unit",
	}
	var out bytes.Buffer
	got := runJSON(cfg, in, &out)
	if got != 1 {
		t.Fatalf("expected exit 1, got %d", got)
	}
	if !strings.Contains(out.String(), "exceed per-package budget") {
		t.Fatalf("expected per-package breach in output, got %q", out.String())
	}
}

func TestRunJSONSoftCapHardBreach(t *testing.T) {
	// One test took 1s, soft cap 100ms (hard cap 300ms) — fail.
	in := strings.NewReader(`
{"Time":"t","Action":"pass","Package":"p","Test":"TestSlow","Elapsed":1.0}
{"Time":"t","Action":"pass","Package":"p","Elapsed":1.0}
`)
	cfg := &config{
		mode:             "json",
		perPackageBudget: 30 * time.Second,
		perTestSoftCap:   100 * time.Millisecond,
		tier:             "unit",
	}
	var out bytes.Buffer
	got := runJSON(cfg, in, &out)
	if got != 1 {
		t.Fatalf("expected exit 1, got %d", got)
	}
	if !strings.Contains(out.String(), "TestSlow") {
		t.Fatalf("expected breaching test in output, got %q", out.String())
	}
}

func TestRunJSONIgnoresNonJSONLines(t *testing.T) {
	// Race-detector banner / build noise lines must not crash the
	// scanner.
	in := strings.NewReader(`
this is not json
WARNING: race detected
{"Time":"t","Action":"pass","Package":"p","Elapsed":0.01}
`)
	cfg := &config{
		mode:             "json",
		perPackageBudget: 30 * time.Second,
		perTestSoftCap:   100 * time.Millisecond,
		tier:             "unit",
	}
	var out bytes.Buffer
	got := runJSON(cfg, in, &out)
	if got != 0 {
		t.Fatalf("expected exit 0, got %d (out=%q)", got, out.String())
	}
}

func TestRunWallUnderBudget(t *testing.T) {
	cfg := &config{
		mode:   "wall",
		tier:   "unit",
		budget: 5 * time.Second,
	}
	var out bytes.Buffer
	got := runWall(cfg, []string{"true"}, &out)
	if got != 0 {
		t.Fatalf("expected exit 0, got %d (out=%q)", got, out.String())
	}
}

func TestRunWallMissingBudget(t *testing.T) {
	cfg := &config{mode: "wall", tier: "unit"}
	var out bytes.Buffer
	got := runWall(cfg, []string{"true"}, &out)
	if got != 2 {
		t.Fatalf("expected exit 2 for missing budget, got %d", got)
	}
}

func TestRunWallCommandFails(t *testing.T) {
	cfg := &config{mode: "wall", tier: "unit", budget: 5 * time.Second}
	var out bytes.Buffer
	got := runWall(cfg, []string{"false"}, &out)
	if got == 0 {
		t.Fatalf("expected non-zero exit when command fails")
	}
}
