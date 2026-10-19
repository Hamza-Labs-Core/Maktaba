// test-budget enforces Story 20.1's wall-clock budgets.
//
// The script runs in two complementary modes:
//
//  1. JSON mode (--mode=json):
//     Reads `go test -json` output from stdin, aggregates per-package
//     elapsed times, and fails if any package exceeds the unit
//     per-package budget (Story 20.1 — 30 s/pkg).
//
//  2. Wall mode (--mode=wall):
//     Times an arbitrary command and fails if its wall-clock exceeds
//     --budget. Used for the integration / e2e / perf-ci tiers where
//     the per-package model doesn't apply.
//
// Both modes also report soft-cap breaches (>3× the per-test cap)
// individually so a single slow test surfaces clearly even when the
// tier total is under budget.
//
// Exit codes:
//
//	0 — under budget, no soft-cap breaches
//	1 — over budget OR a hard-cap breach detected
//	2 — usage / parse error
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// goTestEvent matches the JSON envelope `go test -json` emits.
// Only the fields we read are listed; extras are ignored.
type goTestEvent struct {
	Time    string  `json:"Time"`
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"` // seconds, only set on pass/fail/skip
	Output  string  `json:"Output"`
	// ImportPath is set (instead of Package) on build-output and
	// build-fail events.
	ImportPath string `json:"ImportPath"`
}

// maxFailureLines caps how much of one failure's output is replayed so
// a test that logs megabytes cannot drown the report.
const maxFailureLines = 200

type config struct {
	mode             string
	budget           time.Duration
	perPackageBudget time.Duration
	perTestSoftCap   time.Duration
	tier             string
}

func main() {
	cfg := parseFlags()
	switch cfg.mode {
	case "json":
		os.Exit(runJSON(cfg, os.Stdin, os.Stdout))
	case "wall":
		os.Exit(runWall(cfg, flag.Args(), os.Stdout))
	default:
		fmt.Fprintf(os.Stderr, "test-budget: unknown --mode %q (want json|wall)\n", cfg.mode)
		os.Exit(2)
	}
}

func parseFlags() *config {
	cfg := &config{}
	flag.StringVar(&cfg.mode, "mode", "json", "json|wall")
	flag.StringVar(&cfg.tier, "tier", "unit", "tier name (unit|integration|e2e|perf-ci) for messages")
	flag.DurationVar(&cfg.budget, "budget", 0, "wall-clock budget (wall mode)")
	flag.DurationVar(&cfg.perPackageBudget, "per-package-budget", 30*time.Second,
		"per-package wall-clock budget (json mode)")
	flag.DurationVar(&cfg.perTestSoftCap, "per-test-soft-cap", 100*time.Millisecond,
		"per-test soft cap; >3x is a hard fail (json mode)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr,
			"usage: %s --mode=json [--per-package-budget=30s] [--per-test-soft-cap=100ms] [--tier=NAME]\n"+
				"       %s --mode=wall --budget=2m [--tier=NAME] -- CMD [ARG...]\n",
			os.Args[0], os.Args[0])
	}
	flag.Parse()
	return cfg
}

// --- JSON mode -------------------------------------------------------

type pkgStat struct {
	pkg     string
	elapsed time.Duration
	failed  bool
}

func runJSON(cfg *config, in io.Reader, out io.Writer) int {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)

	pkgs := map[string]*pkgStat{}
	hardCap := time.Duration(3) * cfg.perTestSoftCap
	hardBreaches := []string{}
	// Output is buffered per test (and per package and build) until it
	// ends: dropped on pass/skip, kept on fail so the failing test and
	// what it printed are replayed at the end. Without this the JSON
	// stream swallows every failure message.
	outputs := map[string][]string{}
	failures := []failure{}
	failedTests := map[string]bool{} // package -> had a failing test

	for scanner.Scan() {
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		var ev goTestEvent
		if !strings.HasPrefix(line, "{") || json.Unmarshal([]byte(line), &ev) != nil {
			// Not a go test event (a banner or build noise from a
			// tool): pass it through rather than hide it.
			_, _ = fmt.Fprintln(out, raw)
			continue
		}
		if ev.Package == "" {
			if ev.ImportPath != "" {
				key := "build\x00" + ev.ImportPath
				switch ev.Action {
				case "build-output":
					outputs[key] = append(outputs[key], ev.Output)
				case "build-fail":
					failures = append(failures, failure{name: ev.ImportPath + " [build failed]", output: outputs[key]})
					delete(outputs, key)
				}
			}
			continue
		}
		key := ev.Package + "\x00" + ev.Test
		switch ev.Action {
		case "output":
			outputs[key] = append(outputs[key], ev.Output)
		case "pass", "skip":
			delete(outputs, key)
		case "fail":
			switch {
			case ev.Test != "":
				failedTests[ev.Package] = true
				failures = append(failures, failure{name: ev.Package + "::" + ev.Test, output: outputs[key]})
			case !failedTests[ev.Package]:
				// The package failed without a failing test: a panic
				// outside a test, TestMain, a timeout, a build error.
				failures = append(failures, failure{name: ev.Package + " [package failed]", output: outputs[key]})
			}
			delete(outputs, key)
		}
		ps, ok := pkgs[ev.Package]
		if !ok {
			ps = &pkgStat{pkg: ev.Package}
			pkgs[ev.Package] = ps
		}
		switch ev.Action {
		case "pass", "fail", "skip":
			if ev.Test == "" {
				ps.elapsed = time.Duration(ev.Elapsed * float64(time.Second))
				if ev.Action == "fail" {
					ps.failed = true
				}
			} else if ev.Action != "skip" {
				dur := time.Duration(ev.Elapsed * float64(time.Second))
				if dur > hardCap {
					hardBreaches = append(hardBreaches,
						fmt.Sprintf("%s::%s took %s > %dx soft cap %s",
							ev.Package, ev.Test, dur, 3, cfg.perTestSoftCap))
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		_, _ = fmt.Fprintf(out, "test-budget: scan error: %v\n", err)
		return 2
	}

	// Sort packages by elapsed so the slowest reports first.
	stats := make([]*pkgStat, 0, len(pkgs))
	for _, ps := range pkgs {
		stats = append(stats, ps)
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].elapsed > stats[j].elapsed })

	overBudget := []*pkgStat{}
	var total time.Duration
	for _, ps := range stats {
		total += ps.elapsed
		if ps.elapsed > cfg.perPackageBudget {
			overBudget = append(overBudget, ps)
		}
	}

	_, _ = fmt.Fprintf(out, "test-budget [%s] %d packages, total %s, slowest %s\n",
		cfg.tier, len(stats), total.Round(time.Millisecond),
		describeSlowest(stats))

	if len(failures) > 0 {
		_, _ = fmt.Fprintf(out, "::error::test-budget [%s] %d failing test(s)/package(s):\n", cfg.tier, len(failures))
		for _, f := range failures {
			_, _ = fmt.Fprintf(out, "--- FAIL %s\n", f.name)
			lines := f.output
			if len(lines) > maxFailureLines {
				_, _ = fmt.Fprintf(out, "    ... %d earlier line(s) omitted\n", len(lines)-maxFailureLines)
				lines = lines[len(lines)-maxFailureLines:]
			}
			for _, l := range lines {
				_, _ = fmt.Fprint(out, l)
				if !strings.HasSuffix(l, "\n") {
					_, _ = fmt.Fprintln(out)
				}
			}
		}
	}

	// Test failures are reported above but do not change the exit code:
	// go test's own exit status carries them (tools/test-budget.sh checks
	// both), so this tool's code stays a budget verdict.
	rc := 0
	if len(overBudget) > 0 {
		rc = 1
		_, _ = fmt.Fprintf(out, "::error::test-budget [%s] %d package(s) exceed per-package budget %s:\n",
			cfg.tier, len(overBudget), cfg.perPackageBudget)
		for _, ps := range overBudget {
			_, _ = fmt.Fprintf(out, "  %s: %s\n", ps.pkg, ps.elapsed.Round(time.Millisecond))
		}
	}
	if len(hardBreaches) > 0 {
		rc = 1
		_, _ = fmt.Fprintf(out, "::error::test-budget [%s] %d hard soft-cap breach(es):\n",
			cfg.tier, len(hardBreaches))
		for _, b := range hardBreaches {
			_, _ = fmt.Fprintf(out, "  %s\n", b)
		}
	}
	return rc
}

type failure struct {
	name   string
	output []string
}

func describeSlowest(stats []*pkgStat) string {
	if len(stats) == 0 {
		return "(none)"
	}
	return fmt.Sprintf("%s @ %s", stats[0].pkg, stats[0].elapsed.Round(time.Millisecond))
}

// --- Wall mode -------------------------------------------------------

func runWall(cfg *config, argv []string, out io.Writer) int {
	if cfg.budget <= 0 {
		_, _ = fmt.Fprintln(out, "test-budget: --budget is required in wall mode")
		return 2
	}
	if len(argv) == 0 {
		_, _ = fmt.Fprintln(out, "test-budget: wall mode needs a command after --")
		return 2
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	_, _ = fmt.Fprintf(out, "test-budget [%s] wall=%s budget=%s\n",
		cfg.tier, elapsed.Round(time.Millisecond), cfg.budget)

	rc := 0
	if err != nil {
		// Surface the underlying exit status if we can; otherwise 1.
		if ee, ok := err.(*exec.ExitError); ok {
			rc = ee.ExitCode()
		} else {
			rc = 1
		}
		_, _ = fmt.Fprintf(out, "test-budget [%s] command failed: %v (exit=%d)\n",
			cfg.tier, err, rc)
	}
	if elapsed > cfg.budget {
		_, _ = fmt.Fprintf(out, "::error::test-budget [%s] wall %s > budget %s\n",
			cfg.tier, elapsed.Round(time.Millisecond), cfg.budget)
		if rc == 0 {
			rc = 1
		}
	}
	return rc
}
