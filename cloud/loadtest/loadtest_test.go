package loadtest

import (
	"strings"
	"testing"
	"time"
)

// T03: a user-facing target is refused.
func TestOnlyCIContainersAndStagingAreTargets(t *testing.T) {
	for _, bad := range []string{"https://x.relay.maktaba.tv", "https://maktaba.tv", "https://api.maktaba.tv.", "https://example.com", "https://8.8.8.8", "ftp://relay.test"} {
		if err := CheckTarget(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if err := CheckTarget("https://x.relay.maktaba.tv"); err == nil || !strings.Contains(err.Error(), "never a maktaba.tv name") {
		t.Errorf("the refusal says why: %v", err)
	}
	for _, ok := range []string{"https://staging.relay.maktaba.dev", "http://127.0.0.1:18080", "http://relay-blue:8443", "http://10.60.0.11", "http://relay.test", "http://localhost:8080"} {
		if err := CheckTarget(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
}

// T01: a rule fails the step once it has held for 2 minutes, not before; a let-go restarts it.
func TestAStepFailsWhenARuleHoldsForTwoMinutes(t *testing.T) {
	w := Watch{Rules: DefaultRules}
	t0 := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	for s := 0; s < 115; s += 5 {
		if got := w.Add(Sample{At: at(s), CPU: 0.75}); got != "" {
			t.Fatalf("failed at %ds: %s", s, got)
		}
	}
	// it lets go once: the 2 minutes start again
	w.Add(Sample{At: at(115), CPU: 0.60})
	for s := 120; s < 240; s += 5 {
		if got := w.Add(Sample{At: at(s), CPU: 0.75}); got != "" {
			t.Fatalf("failed at %ds after letting go at 115s: %s", s, got)
		}
	}
	if got := w.Add(Sample{At: at(240), CPU: 0.75}); !strings.HasPrefix(got, "cpu 75%") {
		t.Fatalf("after 2 minutes over 70%%: %q", got)
	}
	// each rule
	for _, s := range []Sample{{Mem: 0.8}, {AddedP95: 25 * time.Millisecond}, {StallRate: 0.006}, {Dropped: 1}} {
		w := Watch{Rules: DefaultRules}
		s.At = t0
		w.Add(s)
		s.At = t0.Add(2 * time.Minute)
		if w.Add(s) == "" {
			t.Errorf("%+v held 2 minutes and passed", s)
		}
	}
}

// T05: 25 % more CPU per Mbit/s fails, and the report shows both numbers.
func TestARegressionFailsAndShowsBothNumbers(t *testing.T) {
	base := Baseline{Sha: "abc1234", CPUSecondsPerMbit: 0.040, MemBytesPerTunnel: 50_000}
	ok, rep := Compare(base, Baseline{CPUSecondsPerMbit: 0.050, MemBytesPerTunnel: 50_000}, Regressed)
	if ok || !strings.Contains(rep, "0.04") || !strings.Contains(rep, "0.05") || !strings.Contains(rep, "WORSE") {
		t.Fatalf("25%% worse passed:\n%s", rep)
	}
	if ok, rep := Compare(base, Baseline{CPUSecondsPerMbit: 0.046, MemBytesPerTunnel: 59_000}, Regressed); !ok {
		t.Fatalf("15%% and 18%% worse failed:\n%s", rep)
	}
	if ok, _ := Compare(base, Baseline{CPUSecondsPerMbit: 0.040, MemBytesPerTunnel: 61_000}, Regressed); ok {
		t.Error("memory 22% worse passed")
	}
	if ok, _ := Compare(Baseline{}, Baseline{CPUSecondsPerMbit: 1}, Regressed); !ok {
		t.Error("no baseline yet (the first run on main) must pass")
	}
}

func TestTheSizingTable(t *testing.T) {
	doc := "# Sizing\n\n| Plan | vCPU / RAM | Idle tunnels at stop | Relayed Mbit/s at stop | Concurrent 5 Mbit/s streams | CPU per 100 Mbit/s | Date, sha |\n|---|---|---|---|---|---|---|\n\nAfter.\n"
	r := Report{Plan: "CPX22", Machine: "2 / 4 GB", Sha: "0123456789abc", At: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC),
		LastGood: &Step{Homes: 6000, Mbit: 420, CPU: 0.66, BitsPerSec: 5_000_000}}
	row := SizingRow(r)
	if row != "| CPX22 | 2 / 4 GB | 6000 | 420 | 84 | 15.7% | 2026-11-07, 012345678 |" {
		t.Fatalf("row %q", row)
	}
	got, err := UpdateSizing(doc, "CPX22", row)
	if err != nil || !strings.Contains(got, "|---|---|---|---|---|---|---|\n"+row+"\n") {
		t.Fatalf("added: %v\n%s", err, got)
	}
	again, _ := UpdateSizing(got, "CPX22", strings.Replace(row, "420", "430", 1))
	if strings.Count(again, "| CPX22 |") != 1 || !strings.Contains(again, "| 430 |") {
		t.Fatalf("replaced in place:\n%s", again)
	}
}

func TestReadingTheMachine(t *testing.T) {
	m, err := ParseDockerStats("143.2%;61.5MiB / 7.6GiB;0.79%", 4)
	if err != nil || m.CPU < 0.357 || m.CPU > 0.359 || m.MemBytes != 61.5*(1<<20) || m.Mem != 0.0079 {
		t.Fatalf("%+v %v", m, err)
	}
	prom := ParseProm(strings.NewReader("# HELP x\nnode_cpu_seconds_total{cpu=\"0\",mode=\"idle\"} 90\nnode_cpu_seconds_total{cpu=\"0\",mode=\"user\"} 10\nmaktaba_relay_active_tunnels 1000\n"))
	if Sum(prom, "node_cpu_seconds_total") != 100 || Sum(prom, "node_cpu_seconds_total", `mode="idle"`) != 90 || prom["maktaba_relay_active_tunnels"] != 1000 {
		t.Fatalf("%v", prom)
	}
	mt := Meter{CPUs: 2}
	t0 := time.Unix(0, 0)
	mt.add(Machine{CPU: 0.5, MemBytes: 10}, t0)
	mt.add(Machine{CPU: 0.5, MemBytes: 30}, t0.Add(10*time.Second))
	if mt.CPUSeconds() != 10 || mt.PeakMem() != 30 {
		t.Fatalf("cpu seconds %v, peak %v", mt.CPUSeconds(), mt.PeakMem())
	}
}
