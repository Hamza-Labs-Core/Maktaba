// affected-tests answers "which tests does this change affect?" for the Maktaba monorepo,
// and gates a push or pull request on it: code that changes needs its tests changed too.
//
// It finds a changed file's tests three ways (and a fourth for the web):
//
//	a. the coverage map: CI measures, on each push to main, which tests execute which
//	   source files (ci.yml's coverage-map job; `affected-tests map` builds it) and
//	   publishes it as the artifact affected-tests-map. It is fetched with gh.
//	b. the UI search: a changed UI file's accessibility identifiers (SwiftUI
//	   .accessibilityIdentifier, Compose testTag, web data-testid) and user-visible
//	   strings, looked up in the tests. A test still using a string that no source has
//	   any more is stale: it will fail.
//	c. naming: foo.go → foo_test.go, Foo.swift → FooTests.swift, foo.py → test_foo.py…
//	   (the web: the test files that import the source, vitest's own graph.)
//
// Subcommands:
//
//	list   (default) the affected tests of a range, by platform
//	gate   list, then the gate's decision: exit 1 when a changed source file has none of
//	       its tests changed, or a test is stale, unless a commit in the range carries a
//	       `No-Test-Change: <reason>` trailer
//	check  gate, then build and run the affected tests that are safe to run here (the
//	       pre-push hook). Never a UI test or a simulator: Apple UI test targets are only
//	       compiled (build-for-testing, unsigned).
//	map    build the coverage map from CI's coverage output
//	fetch-map  fetch the latest map into the cache and print where it is
//
// Exit codes: 0 pass, 1 blocked or a test failed, 2 usage or setup error.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd := "list"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "list", "gate", "check":
		return runAnalyze(cmd, args, stdout, stderr)
	case "map":
		return runMap(args, stdout, stderr)
	case "fetch-map":
		return runFetch(args, stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, "usage: affected-tests [list|gate|check|map|fetch-map] [flags]  (affected-tests <cmd> -h for flags)")
		return 0
	}
	fmt.Fprintf(stderr, "affected-tests: unknown command %q\n", cmd)
	return 2
}

func runAnalyze(cmd string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	rng := fs.String("range", "origin/main...HEAD", "git range: base...head (from the merge base) or base..head")
	mapFlag := fs.String("map", "auto", `coverage map: "auto" (cache, else fetch with gh), "none", or a file`)
	noFetch := fs.Bool("no-fetch", false, "never call gh; use the cached map if there is one")
	asJSON := fs.Bool("json", false, "list as JSON")
	noRun := fs.Bool("no-run", false, "check: gate only, run nothing")
	dir := fs.String("C", ".", "run in this directory (inside the repo)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root, err := repoRoot(*dir)
	if err != nil {
		fmt.Fprintln(stderr, "affected-tests:", err)
		return 2
	}
	r, err := parseRange(*rng)
	if err != nil {
		fmt.Fprintln(stderr, "affected-tests:", err)
		return 2
	}
	chs, err := changes(root, r)
	if err != nil {
		fmt.Fprintln(stderr, "affected-tests:", err)
		return 2
	}
	msgs, err := commitMessages(root, r)
	if err != nil {
		fmt.Fprintln(stderr, "affected-tests:", err)
		return 2
	}
	tree, err := openTree(root, r.Head)
	if err != nil {
		fmt.Fprintln(stderr, "affected-tests:", err)
		return 2
	}
	defer tree.Close()
	m, note := obtainMap(root, *mapFlag, !*noFetch, realGH, time.Now())
	a := analyze(Inputs{Changes: chs, Repo: tree, Map: m, MapNote: note, Messages: msgs})

	if *asJSON {
		if err := printJSON(stdout, a, r.String()); err != nil {
			fmt.Fprintln(stderr, "affected-tests:", err)
			return 2
		}
		if cmd != "list" && a.Blocked() {
			return 1
		}
		return 0
	}
	printList(stdout, a, r.String())
	if cmd == "list" {
		return 0
	}
	printGate(stdout, a)
	if a.Blocked() {
		return 1
	}
	if cmd == "gate" || *noRun {
		return 0
	}
	steps := plan(a, tree, hostEnv(root))
	if len(steps) == 0 {
		fmt.Fprintln(stdout, "\nNothing to run here.")
		return 0
	}
	fmt.Fprintln(stdout, "\nRunning the affected tests that are safe to run here (UI tests: compile only)")
	start := time.Now()
	failed := execute(steps, root, stdout)
	if len(failed) > 0 {
		fmt.Fprintf(stdout, "\nFAILED (%s): %v\n", time.Since(start).Round(time.Second), failed)
		return 1
	}
	fmt.Fprintf(stdout, "\nAll affected tests passed or built (%s).\n", time.Since(start).Round(time.Second))
	return 0
}

func runMap(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("map", flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := MapOptions{Xccov: pairFlag{}, Jacoco: pairFlag{}}
	out := fs.String("out", mapFileName, "where to write the map")
	fs.StringVar(&o.Previous, "previous", "", "an earlier map: its sections this run does not measure are kept")
	fs.StringVar(&o.GoDir, "go", "", "Go coverage directory with index.tsv (tools/affected-tests/collect-go.sh)")
	fs.StringVar(&o.PyJSON, "python", "", "`coverage json --show-contexts` output of a --cov-context=test run")
	fs.StringVar(&o.PyRoot, "python-root", "pipeline", "the directory the Python report's paths are relative to")
	fs.Var(pairFlag(o.Xccov), "xccov", "xccov report JSON=test ID (repeatable), e.g. kit.json=apple-kit:apps/apple/MaktabaKit/Tests/MaktabaKitTests")
	fs.Var(pairFlag(o.Jacoco), "jacoco", "JaCoCo XML=test ID (repeatable), e.g. player.xml=android:apps/android/player/src/test")
	fs.StringVar(&o.Fragments, "fragments", "", "directory of *.xccov.json / *.jacoco.xml, each with a <file>.id holding its test ID")
	fs.StringVar(&o.Commit, "commit", os.Getenv("GITHUB_SHA"), "the commit measured")
	dir := fs.String("C", ".", "the repo")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root, err := repoRoot(*dir)
	if err != nil {
		fmt.Fprintln(stderr, "affected-tests:", err)
		return 2
	}
	o.Root = root
	o.Generated = time.Now().UTC().Format(time.RFC3339)
	tree, err := openTree(root, "HEAD")
	if err != nil {
		fmt.Fprintln(stderr, "affected-tests:", err)
		return 2
	}
	defer tree.Close()
	m, notes, err := buildMap(o, tree.Files())
	if err != nil {
		fmt.Fprintln(stderr, "affected-tests:", err)
		return 2
	}
	for _, n := range notes {
		fmt.Fprintln(stderr, "warning:", n)
	}
	if err := m.save(*out); err != nil {
		fmt.Fprintln(stderr, "affected-tests:", err)
		return 2
	}
	fmt.Fprintf(stdout, "wrote %s\n", *out)
	for _, name := range m.sectionNames() {
		s := m.Sections[name]
		fmt.Fprintf(stdout, "  %-50s %5d source files, %6d tests, measured %s at %s (%s)\n",
			name, len(s.Files), len(s.Tests), s.Generated, short(s.Commit), s.Granularity)
	}
	return 0
}

func runFetch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fetch-map", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("C", ".", "the repo")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	root, err := repoRoot(*dir)
	if err != nil {
		fmt.Fprintln(stderr, "affected-tests:", err)
		return 2
	}
	// a time far ahead: the cache always looks stale, so this fetches
	m, note := obtainMap(root, "auto", true, realGH, time.Now().Add(100*365*24*time.Hour))
	fmt.Fprintln(stdout, note)
	if m == nil {
		return 1
	}
	return 0
}
