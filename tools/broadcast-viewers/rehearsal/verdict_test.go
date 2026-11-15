package rehearsal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T02: a scenario set to expected-failure is reported XFAIL, never as a pass; one that passes
// while listed fails the run (XPASS); an unlisted scenario with no body fails.
func TestT02ExpectedFailures(t *testing.T) {
	exp, err := ParseExpected(strings.NewReader("# comment\n\nbroadcast-observe 50.28 needs the canary\nbroadcast-kill 50.25 being fixed (#1)\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name          string
		built, failed bool
		want          Status
	}{
		{"broadcast-observe", false, true, XFail},
		{"broadcast-kill", true, true, XFail},
		{"broadcast-kill", true, false, XPass},
		{"broadcast-fanout", true, false, Pass},
		{"broadcast-fanout", true, true, Fail},
		{"broadcast-new", false, true, Fail},
	} {
		if got := Judge(c.name, c.built, c.failed, exp); got != c.want {
			t.Errorf("%s built=%v failed=%v: %s, want %s", c.name, c.built, c.failed, got, c.want)
		}
	}
	if !XPass.Counts() || !Fail.Counts() || XFail.Counts() || Pass.Counts() {
		t.Fatal("only fail and xpass count against the run")
	}
	if _, err := ParseExpected(strings.NewReader("broadcast-kill 50.25\n")); err == nil {
		t.Fatal("a line without a reason was taken")
	}

	// a listed scenario that ran and failed: XFAIL in its file and in the report
	r := Result{Scenario: "broadcast-observe", Story: "50.28", Checks: []Check{{Name: "alerts fire", OK: false, Counter: "alerts"}}}
	Finish(&r, true, exp)
	if r.Status != XFail || r.Passed || r.Expected == "" {
		t.Fatalf("%+v", r)
	}
	// a built scenario with no checks is not a pass
	empty := Result{Scenario: "broadcast-fanout"}
	Finish(&empty, true, exp)
	if empty.Status != Fail {
		t.Fatalf("no checks: %s", empty.Status)
	}
	dir := t.TempDir()
	if err := Write(dir, r); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "broadcast-observe.json"))
	if !strings.Contains(string(raw), `"status": "xfail"`) || !strings.Contains(string(raw), `"passed": false`) {
		t.Fatalf("file: %s", raw)
	}
	rep := Report([]Result{r, {Scenario: "broadcast-fanout", Story: "50.21", Status: Pass, Passed: true,
		Checks:  []Check{{Name: "home PUTs", OK: true}},
		Timings: map[string]float64{"run": 12},
		Fanout:  []FanoutRow{{Viewers: 100, Seconds: 60, Segments: 15, Rungs: 3, HomePuts: 45, R2Reads: 200, R2MaxPerLoc: 1}}}})
	for _, want := range []string{"| broadcast-observe | 50.28 | XFAIL (expected: 50.28 needs the canary) |", "alerts fire [alerts]",
		"| broadcast-fanout | 50.21 | PASS | run 12.0 s |", "| 100 | 60 | 15 × 3 | 45 |"} {
		if !strings.Contains(rep, want) {
			t.Errorf("report lacks %q:\n%s", want, rep)
		}
	}
}

// The repository's own listing parses, and names only scenarios the rehearsal knows.
func TestTheListingParses(t *testing.T) {
	f, err := os.Open("../../rehearsal-cloud/broadcast/expected-failures.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	exp, err := ParseExpected(f)
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, s := range Scenarios {
		known[s.Name] = true
	}
	for name := range exp {
		if !known[name] {
			t.Errorf("expected-failures.txt names %q, which is not a scenario", name)
		}
	}
}

// The CI matrix: the pull request's set has the fan-out, its self-test and every functional
// scenario with a stack, and reports any unbuilt one without; the nightly set adds the 24 h and
// Hetzner ones.
func TestTheSets(t *testing.T) {
	has := func(xs []string, x string) bool {
		for _, v := range xs {
			if v == x {
				return true
			}
		}
		return false
	}
	pr, prOff := InSet("pr", true), InSet("pr", false)
	night, nightOff := InSet("nightly", true), InSet("nightly", false)
	if !has(pr, "broadcast-fanout") || !has(pr, "broadcast-fanout-planted") || has(pr, "broadcast-home-offline-24h") {
		t.Fatalf("pr set: %v", pr)
	}
	if !has(night, "broadcast-home-offline-24h") || !has(night, "broadcast-hetzner-down") || has(pr, "broadcast-hetzner-down") || has(prOff, "broadcast-hetzner-down") {
		t.Fatalf("nightly: %v / %v; pr %v, unbuilt %v", night, nightOff, pr, prOff)
	}
	for _, n := range []string{"broadcast-plans", "broadcast-publishable", "broadcast-access", "broadcast-takedown", "broadcast-observe"} {
		if !has(pr, n) || !has(night, n) {
			t.Errorf("%s runs in both sets with a stack: pr %v, nightly %v", n, pr, night)
		}
	}
	if len(night)+len(nightOff) != len(Scenarios) {
		t.Fatalf("the nightly set is every scenario: %d + %d of %d", len(night), len(nightOff), len(Scenarios))
	}
	// an unbuilt scenario must be listed (else it fails); a built one listed is XFAIL or XPASS
	f, err := os.Open("../../rehearsal-cloud/broadcast/expected-failures.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	exp, err := ParseExpected(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nightOff {
		if _, ok := exp[n]; !ok {
			t.Errorf("%s has no body and is not in expected-failures.txt", n)
		}
	}
}
