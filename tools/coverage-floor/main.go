// coverage-floor enforces the Story 20.3 AC1 / TC1 coverage gate:
// per-module statement-coverage floors that block a PR when coverage
// regresses below a documented threshold.
//
// The gap doc (Epic 20, Story 20.3) records this as a top gap:
// "no tools/coverage/, no floors gate in CI, no coverage collected in
// any CI workflow". This tool is that gate, mirroring the existing
// tools/log-lint / tools/test-budget convention: a standalone Go tool,
// stdlib only, human-readable output, exit 0 = all floors met /
// non-zero = a floor was breached, and it does not bail on the first
// failure (every breach is reported so one CI run shows them all).
//
// The floors are a NON-BREAKING RATCHET: each floor is set at (or just
// below) the coverage measured on the integrated branch at the time
// the gate landed, so turning the gate on cannot redden an in-flight
// PR. Floors are only ever raised, never lowered, as coverage improves.
// See tools/coverage-floor/floors.yaml for the values and rationale.
//
// Usage:
//
//	coverage-floor --floors=<file> \
//	    --report=<label>=<percent> [--report=...] \
//	    --profile=<label>=<coverprofile> [--profile=...]
//
// A --profile entry points at a `go test -coverprofile` file; the tool
// parses the standard cover profile format and computes the statement
// coverage percentage for that label. A --report entry supplies an
// already-computed percentage for that label (used for the Python /
// pytest-cov lane where coverage is produced by a different toolchain).
// Every label present in the floors config must be satisfied by either
// a --profile or a --report; a label with no input is a hard error so
// a silently-dropped lane can never be a false green.
//
// The floors file is a minimal `label: percent` document (one entry
// per line, `#` comments and blank lines ignored). It is named
// floors.yaml for familiarity, but only this flat subset is parsed —
// no external YAML dependency, matching the stdlib-only tool policy.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// repeatedFlag collects --profile/--report key=value pairs.
type repeatedFlag []string

func (r *repeatedFlag) String() string { return strings.Join(*r, ",") }
func (r *repeatedFlag) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func main() {
	var floorsPath string
	var profiles repeatedFlag
	var reports repeatedFlag
	flag.StringVar(&floorsPath, "floors", "tools/coverage-floor/floors.yaml",
		"Path to the floors config (flat `label: percent` lines)")
	flag.Var(&profiles, "profile",
		"label=path to a go test -coverprofile file (repeatable)")
	flag.Var(&reports, "report",
		"label=percent already-computed coverage for a label (repeatable)")
	flag.Parse()

	floors, err := parseFloors(floorsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coverage-floor: %v\n", err)
		os.Exit(2)
	}
	if len(floors) == 0 {
		fmt.Fprintln(os.Stderr, "coverage-floor: floors config is empty")
		os.Exit(2)
	}

	// Resolve the measured percentage for every label.
	measured := map[string]float64{}

	for _, p := range profiles {
		label, path, ok := splitKV(p)
		if !ok {
			fmt.Fprintf(os.Stderr, "coverage-floor: bad --profile %q (want label=path)\n", p)
			os.Exit(2)
		}
		pct, perr := profileCoverage(path)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "coverage-floor: %s: %v\n", label, perr)
			os.Exit(2)
		}
		measured[label] = pct
	}

	for _, r := range reports {
		label, raw, ok := splitKV(r)
		if !ok {
			fmt.Fprintf(os.Stderr, "coverage-floor: bad --report %q (want label=percent)\n", r)
			os.Exit(2)
		}
		pct, perr := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(raw), "%"), 64)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "coverage-floor: %s: bad percent %q\n", label, raw)
			os.Exit(2)
		}
		measured[label] = pct
	}

	// Check every configured floor. A configured label with no measured
	// input is a hard failure — a dropped coverage lane must never pass.
	labels := make([]string, 0, len(floors))
	for l := range floors {
		labels = append(labels, l)
	}
	sort.Strings(labels)

	failed := false
	for _, label := range labels {
		floor := floors[label]
		got, present := measured[label]
		if !present {
			fmt.Printf("MISSING %-26s floor %.1f%% — no --profile/--report supplied (gate cannot verify; treated as failure)\n",
				label, floor)
			failed = true
			continue
		}
		if got+1e-9 < floor {
			fmt.Printf("FAIL    %-26s %.1f%% < floor %.1f%%\n", label, got, floor)
			failed = true
		} else {
			fmt.Printf("ok      %-26s %.1f%% >= floor %.1f%%\n", label, got, floor)
		}
	}

	if failed {
		fmt.Fprintln(os.Stderr, "coverage-floor: one or more coverage floors breached (Story 20.3 AC1/TC1)")
		os.Exit(1)
	}
	fmt.Println("coverage-floor: all coverage floors met")
}

func splitKV(s string) (key, val string, ok bool) {
	i := strings.IndexByte(s, '=')
	if i < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(s[:i])
	val = strings.TrimSpace(s[i+1:])
	if key == "" || val == "" {
		return "", "", false
	}
	return key, val, true
}

// parseFloors reads the flat `label: percent` config. Lines that are
// blank or start with `#` are ignored.
func parseFloors(path string) (map[string]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open floors %q: %w", path, err)
	}
	defer f.Close()

	out := map[string]float64{}
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		i := strings.IndexByte(text, ':')
		if i < 0 {
			return nil, fmt.Errorf("floors %s:%d: missing ':' in %q", path, line, text)
		}
		label := strings.TrimSpace(text[:i])
		// Allow a trailing `# comment` after the value.
		valPart := text[i+1:]
		if h := strings.IndexByte(valPart, '#'); h >= 0 {
			valPart = valPart[:h]
		}
		val := strings.TrimSuffix(strings.TrimSpace(valPart), "%")
		pct, perr := strconv.ParseFloat(val, 64)
		if perr != nil {
			return nil, fmt.Errorf("floors %s:%d: bad percent %q", path, line, val)
		}
		if label == "" {
			return nil, fmt.Errorf("floors %s:%d: empty label", path, line)
		}
		out[label] = pct
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read floors %q: %w", path, err)
	}
	return out, nil
}

// profileCoverage parses a Go cover profile and returns the overall
// statement coverage percentage, computed the same way `go tool cover
// -func` reports the total: covered statements / total statements.
//
// Profile format (after the first `mode:` line):
//
//	name.go:line.col,line.col numStmt count
//
// A block counts as covered when count > 0.
func profileCoverage(path string) (float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open profile %q: %w", path, err)
	}
	defer f.Close()

	var total, covered int64
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		if first {
			first = false
			if strings.HasPrefix(text, "mode:") {
				continue
			}
			// No mode header — fall through and parse this line too.
		}
		numStmt, count, perr := parseProfileLine(text)
		if perr != nil {
			return 0, fmt.Errorf("profile %q: %w", path, perr)
		}
		total += numStmt
		if count > 0 {
			covered += numStmt
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("read profile %q: %w", path, err)
	}
	if total == 0 {
		// No statements at all means nothing was measured — refuse to
		// pass silently (an empty profile must not be a false green).
		return 0, fmt.Errorf("profile has zero statements (no coverage measured)")
	}
	return float64(covered) / float64(total) * 100.0, nil
}

// parseProfileLine extracts numStmt and count from one cover-profile
// data line: `name.go:l.c,l.c numStmt count`.
func parseProfileLine(line string) (numStmt, count int64, err error) {
	// Split off the file:range prefix at the first space after the
	// final colon-delimited range. The last two whitespace-separated
	// fields are numStmt and count.
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return 0, 0, fmt.Errorf("malformed profile line %q", line)
	}
	count, err = strconv.ParseInt(fields[len(fields)-1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("bad count in %q: %w", line, err)
	}
	numStmt, err = strconv.ParseInt(fields[len(fields)-2], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("bad numStmt in %q: %w", line, err)
	}
	return numStmt, count, nil
}
