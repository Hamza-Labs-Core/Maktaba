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
	if !strings.Contains(out.String(), "WARNING: race detected") {
		t.Fatalf("non-JSON lines must pass through, got %q", out.String())
	}
}

func unitCfg() *config {
	return &config{
		mode:             "json",
		perPackageBudget: 30 * time.Second,
		perTestSoftCap:   100 * time.Millisecond,
		tier:             "unit",
	}
}

// A failing test is named and its output replayed; a passing test's
// output is not.
func TestRunJSONReplaysFailingTestOutput(t *testing.T) {
	in := strings.NewReader(`
{"Action":"run","Package":"p","Test":"TestOK"}
{"Action":"output","Package":"p","Test":"TestOK","Output":"=== RUN   TestOK\n"}
{"Action":"output","Package":"p","Test":"TestOK","Output":"    ok_test.go:1: quiet\n"}
{"Action":"pass","Package":"p","Test":"TestOK","Elapsed":0}
{"Action":"run","Package":"p","Test":"TestBad"}
{"Action":"output","Package":"p","Test":"TestBad","Output":"=== RUN   TestBad\n"}
{"Action":"output","Package":"p","Test":"TestBad","Output":"    bad_test.go:9: want 1, got 2\n"}
{"Action":"output","Package":"p","Test":"TestBad","Output":"--- FAIL: TestBad (0.00s)\n"}
{"Action":"fail","Package":"p","Test":"TestBad","Elapsed":0}
{"Action":"output","Package":"p","Output":"FAIL\n"}
{"Action":"fail","Package":"p","Elapsed":0.01}
`)
	var out bytes.Buffer
	if got := runJSON(unitCfg(), in, &out); got != 0 {
		t.Fatalf("test failures are go test's exit code, not a budget breach: got %d", got)
	}
	s := out.String()
	for _, want := range []string{"1 failing test(s)", "--- FAIL p::TestBad", "bad_test.go:9: want 1, got 2"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in %q", want, s)
		}
	}
	if strings.Contains(s, "quiet") || strings.Contains(s, "[package failed]") {
		t.Fatalf("passing output or a duplicate package failure reported: %q", s)
	}
}

// A package that fails with no failing test (a panic in TestMain, a
// build error) is reported with its own output.
func TestRunJSONReportsPackageAndBuildFailures(t *testing.T) {
	in := strings.NewReader(`
{"ImportPath":"q [q.test]","Action":"build-output","Output":"q/x.go:3:1: syntax error\n"}
{"ImportPath":"q [q.test]","Action":"build-fail"}
{"Action":"output","Package":"r","Output":"panic: boom in TestMain\n"}
{"Action":"fail","Package":"r","Elapsed":0.01}
`)
	var out bytes.Buffer
	runJSON(unitCfg(), in, &out)
	s := out.String()
	for _, want := range []string{"q [q.test] [build failed]", "syntax error", "r [package failed]", "panic: boom in TestMain"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in %q", want, s)
		}
	}
}

// A test that hangs until go test's -timeout gets no fail event (only its
// package does), and the timeout panic is that test's output: it is named
// and its output replayed. A test that ended is not. (The stream is what
// go1.25 test2json writes for a test sleeping past -timeout 2s.)
func TestRunJSONReportsTestThatDidNotFinish(t *testing.T) {
	in := strings.NewReader(`
{"Action":"start","Package":"tb"}
{"Action":"run","Package":"tb","Test":"TestQuick"}
{"Action":"output","Package":"tb","Test":"TestQuick","Output":"=== RUN   TestQuick\n"}
{"Action":"output","Package":"tb","Test":"TestQuick","Output":"--- PASS: TestQuick (0.00s)\n"}
{"Action":"pass","Package":"tb","Test":"TestQuick","Elapsed":0}
{"Action":"run","Package":"tb","Test":"TestHang"}
{"Action":"output","Package":"tb","Test":"TestHang","Output":"=== RUN   TestHang\n"}
{"Action":"output","Package":"tb","Test":"TestHang","Output":"panic: test timed out after 2s\n"}
{"Action":"output","Package":"tb","Test":"TestHang","Output":"\trunning tests:\n"}
{"Action":"output","Package":"tb","Test":"TestHang","Output":"\t\tTestHang (2s)\n"}
{"Action":"output","Package":"tb","Test":"TestHang","Output":"goroutine 7 [sleep]:\n"}
{"Action":"output","Package":"tb","Output":"FAIL\ttb\t2.464s\n"}
{"Action":"fail","Package":"tb","Elapsed":2.464}
`)
	var out bytes.Buffer
	runJSON(unitCfg(), in, &out)
	s := out.String()
	for _, want := range []string{"--- FAIL tb::TestHang [did not finish]", "panic: test timed out after 2s", "goroutine 7 [sleep]"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in %q", want, s)
		}
	}
	if strings.Contains(s, "TestQuick") {
		t.Fatalf("a test that passed is reported: %q", s)
	}
}

// A package that passes forgets the tests it saw start.
func TestRunJSONPassedPackageReportsNothing(t *testing.T) {
	in := strings.NewReader(`
{"Action":"run","Package":"p","Test":"TestA"}
{"Action":"output","Package":"p","Test":"TestA","Output":"=== RUN   TestA\n"}
{"Action":"run","Package":"p","Test":"TestA/sub"}
{"Action":"pass","Package":"p","Test":"TestA/sub","Elapsed":0}
{"Action":"pass","Package":"p","Test":"TestA","Elapsed":0}
{"Action":"pass","Package":"p","Elapsed":0.01}
`)
	var out bytes.Buffer
	runJSON(unitCfg(), in, &out)
	if s := out.String(); strings.Contains(s, "FAIL") {
		t.Fatalf("a passing package reported a failure: %q", s)
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
