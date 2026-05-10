package perf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validYAML = `
version: 1
hardware_profiles:
  linux-x86-16gb: server
endpoints:
  libraries:
    surface: rest
    method: GET
    path: /api/libraries
    profile: linux-x86-16gb
    cache: warm
    p50_ms: 30
    p95_ms: 80
    p99_ms: 150
    ci_pr: true
  search_cold:
    surface: rest
    method: POST
    path: /api/search
    profile: linux-x86-16gb
    cache: cold
    p95_ms: 1500
    ci_pr: false
throughputs:
  scan:
    profile: linux-x86-16gb
    target: 50
`

func writeTemp(t *testing.T, s string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "b.yaml")
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	bg, err := Load(writeTemp(t, validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if bg.Endpoints["libraries"].P95ms != 80 {
		t.Fatalf("p95 mismatch")
	}
	subset := bg.CISubset()
	if len(subset) != 1 || subset[0].Path != "/api/libraries" {
		t.Fatalf("CISubset wrong: %+v", subset)
	}
}

func TestValidateRejectsP99LessThanP95(t *testing.T) {
	bad := strings.Replace(validYAML, "p99_ms: 150", "p99_ms: 50", 1)
	_, err := Load(writeTemp(t, bad))
	if err == nil || !strings.Contains(err.Error(), "p99") {
		t.Fatalf("expected p99<p95 rejection, got %v", err)
	}
}

func TestValidateRejectsUnknownProfile(t *testing.T) {
	bad := strings.Replace(validYAML, "linux-x86-16gb", "mystery-box", 1)
	_, err := Load(writeTemp(t, bad))
	if err == nil {
		t.Fatal("expected unknown-profile rejection")
	}
}

func TestValidateRejectsMissingP95(t *testing.T) {
	bad := strings.Replace(validYAML, "p95_ms: 80\n    p99_ms: 150", "p99_ms: 150", 1)
	_, err := Load(writeTemp(t, bad))
	if err == nil {
		t.Fatal("expected missing-p95 rejection")
	}
}

func TestLookup(t *testing.T) {
	bg, _ := Load(writeTemp(t, validYAML))
	got := bg.Lookup("GET", "/api/libraries", "warm")
	if got == nil || got.P95ms != 80 {
		t.Fatalf("Lookup result: %+v", got)
	}
	if bg.Lookup("GET", "/nope", "warm") != nil {
		t.Fatal("Lookup should miss")
	}
}

func TestGaugeReportsBreach(t *testing.T) {
	bg, _ := Load(writeTemp(t, validYAML))
	g := NewGauge()
	// 80 fast + 20 slow; p95 lands inside the slow tail (budget = 80ms).
	for i := 0; i < 80; i++ {
		g.Observe("libraries", 10*time.Millisecond)
	}
	for i := 0; i < 20; i++ {
		g.Observe("libraries", 500*time.Millisecond)
	}
	breaches := g.Report(bg)
	if len(breaches) != 1 {
		t.Fatalf("expected 1 breach, got %+v", breaches)
	}
	if breaches[0].Endpoint != "libraries" {
		t.Fatalf("wrong endpoint: %s", breaches[0].Endpoint)
	}
}

func TestGaugeNoBreachUnderBudget(t *testing.T) {
	bg, _ := Load(writeTemp(t, validYAML))
	g := NewGauge()
	for i := 0; i < 100; i++ {
		g.Observe("libraries", 20*time.Millisecond)
	}
	if got := g.Report(bg); len(got) != 0 {
		t.Fatalf("did not expect breaches: %+v", got)
	}
}

func TestLoadCanonicalRepoFile(t *testing.T) {
	// Real budget file should parse + validate.
	_, err := Load("../../../shared/perf_budgets.yaml")
	if err != nil {
		t.Fatalf("real perf_budgets.yaml failed: %v", err)
	}
}
