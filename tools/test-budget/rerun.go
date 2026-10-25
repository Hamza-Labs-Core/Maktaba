package main

// Re-running a hard-cap breach alone.
//
// The unit tier runs a module's packages in parallel (go test -p defaults
// to GOMAXPROCS) under -race, so a test's elapsed time is its own work
// plus what the other packages' test binaries take from the runner. On
// the 4-vCPU CI runner a different test crossed the 1.2 s hard cap each
// time (1.21-1.24 s), tests whose work is a fraction of that. So a breach
// by a test that passed is re-run once, alone: the same go test flags,
// -p 1, -run anchored to exactly that test, in its package. The breach
// stands (fails the build) only if the test is past the hard cap there
// too, fails there, or does not report. One that does not repeat is a
// ::warning:: naming both timings, so a test drifting toward the cap
// stays visible. More than rerunMax tests to re-run in one module are
// re-run together, one go test per package with -p 1 -parallel 1: each
// test then runs alone, one after the other (a package's t.Parallel tests
// stretch each other's time under -race on a loaded runner), and a real
// slowdown still repeats there and stands.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// hardCapFactor: a test past this many times the per-test soft cap
// breaches the hard cap (Story 20.1 AC4).
const hardCapFactor = 3

// defaultRerunMax is --rerun-max's default.
const defaultRerunMax = 10

// breach is a test past the hard cap in the run being read.
type breach struct {
	pkg, test string
	elapsed   time.Duration
	failed    bool // the test failed: go test fails the build; not re-run
}

func (b breach) describe(softCap time.Duration) string {
	return fmt.Sprintf("%s::%s took %s > %dx soft cap %s", b.pkg, b.test, b.elapsed, hardCapFactor, softCap)
}

// verdict is what became of a breach: the line that reports it and,
// when the re-run failed or did not report the test, its output.
type verdict struct {
	line   string
	output []string
	stands bool // fails the build
}

// runner runs argv in dir and returns what it wrote to stdout and
// stderr; err is set when it could not start or exited non-zero.
type runner func(dir string, argv []string) ([]byte, error)

func execRunner(dir string, argv []string) ([]byte, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// judgeBreaches settles each breach, in order. Without a go test command
// to re-run, every breach stands.
func judgeBreaches(cfg *config, hardCap time.Duration, breaches []breach, out io.Writer) []verdict {
	verdicts := make([]verdict, len(breaches))
	for i, b := range breaches {
		verdicts[i] = verdict{line: b.describe(cfg.perTestSoftCap), stands: true}
	}
	if len(cfg.rerunCmd) == 0 {
		return verdicts
	}

	// A breach whose parent test breached too is settled by the parent's
	// re-run, which runs it as well: a serial subtest's time is part of
	// its parent's, so the two breach together.
	runs := []int{}            // the breaches re-run, in order
	settles := map[int][]int{} // re-run breach -> the breaches it settles
	for i, b := range breaches {
		if b.failed {
			verdicts[i].line += " (the test failed: not re-run)"
			continue
		}
		top := outermost(breaches, i)
		if top == i {
			runs = append(runs, i)
		}
		settles[top] = append(settles[top], i)
	}
	run := cfg.run
	if run == nil {
		run = execRunner
	}
	if len(runs) > cfg.rerunMax {
		_, _ = fmt.Fprintf(out, "test-budget [%s] %d tests past the hard cap %s, more than %d: re-run together, one at a time\n",
			cfg.tier, len(runs), hardCap, cfg.rerunMax)
		batchRerun(cfg, run, hardCap, breaches, runs, settles, verdicts, out)
		return verdicts
	}
	for _, r := range runs {
		b := breaches[r]
		argv := append(append([]string{}, cfg.rerunCmd...), "-p", "1", "-run", runPattern(b.test), b.pkg)
		_, _ = fmt.Fprintf(out, "test-budget [%s] re-running %s::%s alone: %s\n",
			cfg.tier, b.pkg, b.test, strings.Join(argv, " "))
		stdout, err := run(cfg.rerunDir, argv)
		results, other := parseRerun(stdout, b.pkg)
		for _, i := range settles[r] {
			verdicts[i] = settle(breaches[i], cfg.perTestSoftCap, hardCap, results, other, err)
		}
	}
	return verdicts
}

// batchRerun settles the re-run breaches with one go test per package:
// -p 1 -parallel 1, -run anchored to their top-level tests, so each runs
// alone, in turn (a top-level test runs its subtests too).
func batchRerun(cfg *config, run runner, hardCap time.Duration, breaches []breach, runs []int,
	settles map[int][]int, verdicts []verdict, out io.Writer) {
	pkgs := []string{}
	tops := map[string][]string{} // package -> its top-level tests, in order
	for _, r := range runs {
		b := breaches[r]
		top, _, _ := strings.Cut(b.test, "/")
		if _, ok := tops[b.pkg]; !ok {
			pkgs = append(pkgs, b.pkg)
		}
		if !slices.Contains(tops[b.pkg], top) {
			tops[b.pkg] = append(tops[b.pkg], top)
		}
	}
	for _, pkg := range pkgs {
		names := make([]string, len(tops[pkg]))
		for i, n := range tops[pkg] {
			names[i] = strings.TrimSuffix(strings.TrimPrefix(runPattern(n), "^"), "$")
		}
		argv := append(append([]string{}, cfg.rerunCmd...), "-p", "1", "-parallel", "1",
			"-run", "^("+strings.Join(names, "|")+")$", pkg)
		_, _ = fmt.Fprintf(out, "test-budget [%s] re-running %d tests of %s alone, in turn: %s\n",
			cfg.tier, len(names), pkg, strings.Join(argv, " "))
		stdout, err := run(cfg.rerunDir, argv)
		results, other := parseRerun(stdout, pkg)
		for _, r := range runs {
			if breaches[r].pkg != pkg {
				continue
			}
			for _, i := range settles[r] {
				verdicts[i] = settle(breaches[i], cfg.perTestSoftCap, hardCap, results, other, err)
			}
		}
	}
}

// outermost is the outermost breach by a passing test that holds breach
// i (a parent test in the same package), or i itself.
func outermost(breaches []breach, i int) int {
	top := i
	for j, c := range breaches {
		if !c.failed && c.pkg == breaches[i].pkg &&
			strings.HasPrefix(breaches[i].test, c.test+"/") &&
			len(c.test) < len(breaches[top].test) {
			top = j
		}
	}
	return top
}

// settle is breach b's verdict from the re-run that ran it alone.
func settle(b breach, softCap, hardCap time.Duration, results map[string]testResult, other []string, err error) verdict {
	v := verdict{line: b.describe(softCap), stands: true}
	res, ok := results[b.test]
	switch {
	case !ok:
		v.line += "; re-run alone: not reported"
		if err != nil {
			v.line += " (" + err.Error() + ")"
		}
		v.output = other
	case res.action == "fail":
		v.line += "; re-run alone: failed"
		v.output = res.output
	case res.action != "pass":
		v.line += "; re-run alone: " + res.action
	case res.elapsed > hardCap:
		v.line += fmt.Sprintf("; re-run alone: %s", res.elapsed)
	default:
		v.stands = false
		v.line = fmt.Sprintf("%s::%s took %s > %dx soft cap %s with the other packages running, %s re-run alone: the runner's load, not the test",
			b.pkg, b.test, b.elapsed, hardCapFactor, softCap, res.elapsed)
	}
	return v
}

// testResult is how a test ended in a re-run.
type testResult struct {
	action  string // pass, fail or skip
	elapsed time.Duration
	output  []string
}

// parseRerun reads a re-run's go test -json output: how each test of pkg
// ended, and the lines that are no test's (build errors, package output,
// go's own messages) to show when the test never reports.
func parseRerun(stdout []byte, pkg string) (map[string]testResult, []string) {
	results := map[string]testResult{}
	outputs := map[string][]string{}
	other := []string{}
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		raw := sc.Text()
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		var ev goTestEvent
		if !strings.HasPrefix(line, "{") || json.Unmarshal([]byte(line), &ev) != nil {
			other = append(other, raw)
			continue
		}
		if ev.Package != pkg || ev.Test == "" {
			if ev.Action == "output" || ev.Action == "build-output" {
				other = append(other, ev.Output)
			}
			continue
		}
		switch ev.Action {
		case "output":
			outputs[ev.Test] = append(outputs[ev.Test], ev.Output)
		case "pass", "fail", "skip":
			results[ev.Test] = testResult{
				action:  ev.Action,
				elapsed: time.Duration(ev.Elapsed * float64(time.Second)),
				output:  outputs[ev.Test],
			}
			delete(outputs, ev.Test)
		}
	}
	if err := sc.Err(); err != nil {
		other = append(other, "test-budget: reading the re-run: "+err.Error())
	}
	return results, other
}

// runPattern is the go test -run pattern that selects exactly test: each
// /-separated level quoted and anchored, TestX/a_b -> ^TestX$/^a_b$.
// -run matches each level against the name go test reports, in which
// t.Run has rewritten spaces to _ (as the names in the JSON stream are).
func runPattern(test string) string {
	levels := strings.Split(test, "/")
	for i, l := range levels {
		l = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return '_'
			}
			return r
		}, l)
		levels[i] = "^" + regexp.QuoteMeta(l) + "$"
	}
	return strings.Join(levels, "/")
}
