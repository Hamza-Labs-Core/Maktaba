// Package rehearsal is the broadcast rehearsal (Epic 50 story 50.29): the stack of
// tools/rehearsal-cloud/broadcast (the edge in Miniflare, a home running the streaming service's
// broadcaster) under the simulated viewers of tools/broadcast-viewers, each scenario judged by its
// checks and its listing in expected-failures.txt, and one report for the CI artifacts.
//
// This file is the verdict and the report (unit-tested without Docker, T02); the scenarios are
// scenarios_test.go, built with -tags broadcastrehearsal.
package rehearsal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	viewers "github.com/Hamza-Labs-Core/Maktaba/tools/broadcast-viewers"
)

// Status is a scenario's verdict.
type Status string

const (
	Pass Status = "pass"
	Fail Status = "fail"
	// XFail is a listed scenario that failed (or has no body yet): reported, not counted.
	XFail Status = "xfail"
	// XPass is a listed scenario that passed: the run fails until its line goes.
	XPass Status = "xpass"
)

// Counts says whether a status fails the run.
func (s Status) Counts() bool { return s == Fail || s == XPass }

// Check is one of a scenario's pass conditions. Counter names what was measured, so a failure
// says which number broke (the planted regression must fail on r2_reads, T03).
type Check struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail,omitempty"`
	Counter string `json:"counter,omitempty"`
}

// FanoutRow is one step of the fan-out: viewers against the home's upload and R2's reads.
type FanoutRow struct {
	Viewers      int     `json:"viewers"`
	Seconds      int     `json:"seconds"`
	Segments     int     `json:"segments"`
	Rungs        int     `json:"rungs"`
	HomePuts     int     `json:"home_puts"`
	HomeBytes    int64   `json:"home_bytes"`
	EdgePuts     int     `json:"edge_puts"`
	R2Reads      int     `json:"r2_reads"`
	R2MaxPerLoc  int     `json:"r2_max_per_object_per_location"`
	DOGets       int     `json:"do_gets"`
	ViewerSegs   int     `json:"viewer_segments"`
	ViewerErrors int     `json:"viewer_errors"`
	FetchP95     float64 `json:"fetch_p95_ms"`
	AfterHomeP95 float64 `json:"after_home_p95_ms"`
}

// Result is one scenario's record (results/<scenario>.json), in the cloud rehearsal's spirit.
type Result struct {
	Scenario string             `json:"scenario"`
	Story    string             `json:"story"`
	Status   Status             `json:"status"`
	Passed   bool               `json:"passed"`
	Expected string             `json:"expected_failure,omitempty"`
	Checks   []Check            `json:"checks"`
	Timings  map[string]float64 `json:"timings_s,omitempty"`
	Fanout   []FanoutRow        `json:"fanout,omitempty"`
	Latency  map[string]any     `json:"latency_ms,omitempty"`
	Notes    []string           `json:"notes,omitempty"`
	Seconds  float64            `json:"seconds"`
}

// Failed is the checks that failed.
func (r Result) Failed() []Check {
	var out []Check
	for _, c := range r.Checks {
		if !c.OK {
			out = append(out, c)
		}
	}
	return out
}

// Expected is expected-failures.txt: scenario → "<story> <why>".
type Expected map[string]string

// ParseExpected reads the listing; comments and blank lines are skipped.
func ParseExpected(r io.Reader) (Expected, error) {
	out := Expected{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 {
			return nil, fmt.Errorf("expected-failures: %q needs a scenario, a story and why", line)
		}
		out[f[0]] = strings.Join(f[1:], " ")
	}
	return out, sc.Err()
}

// Judge is a scenario's verdict: built says it has a body, failed that a check failed (or it
// could not run). A listed scenario is never a pass; an unlisted one without a body fails.
func Judge(name string, built, failed bool, exp Expected) Status {
	_, listed := exp[name]
	switch {
	case !built && listed:
		return XFail
	case !built:
		return Fail // a scenario with no body must be listed
	case listed && failed:
		return XFail
	case listed:
		return XPass
	case failed:
		return Fail
	}
	return Pass
}

// Finish sets a result's status from its checks.
func Finish(r *Result, built bool, exp Expected) {
	failed := len(r.Failed()) > 0 || (built && len(r.Checks) == 0)
	r.Status = Judge(r.Scenario, built, failed, exp)
	r.Passed = r.Status == Pass
	r.Expected = exp[r.Scenario]
}

// Write saves a result as <dir>/<scenario>.json.
func Write(dir string, r Result) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, r.Scenario+".json"), append(raw, '\n'), 0o644)
}

// ReadResult reads a result file back.
func ReadResult(path string) (Result, error) {
	var r Result
	raw, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(raw, &r)
	return r, err
}

// Report renders the results as the run's Markdown report: each scenario's verdict and timings,
// the fan-out table (viewers vs home bytes vs R2 reads) and the latency percentiles.
func Report(results []Result) string {
	sort.Slice(results, func(i, j int) bool { return results[i].Scenario < results[j].Scenario })
	var b strings.Builder
	b.WriteString("## Broadcast rehearsal (story 50.29)\n\n| Scenario | Story | Verdict | Timings | Failed checks |\n|---|---|---|---|---|\n")
	for _, r := range results {
		verdict := strings.ToUpper(string(r.Status))
		if r.Status == XFail {
			verdict += " (expected: " + r.Expected + ")"
		}
		var ts []string
		keys := make([]string, 0, len(r.Timings))
		for k := range r.Timings {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ts = append(ts, fmt.Sprintf("%s %.1f s", k, r.Timings[k]))
		}
		var failed []string
		for _, c := range r.Failed() {
			s := c.Name
			if c.Counter != "" {
				s += " [" + c.Counter + "]"
			}
			if c.Detail != "" {
				s += ": " + c.Detail
			}
			failed = append(failed, s)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", r.Scenario, r.Story, verdict, cell(strings.Join(ts, "; ")), cell(strings.Join(failed, "; ")))
	}
	for _, r := range results {
		if len(r.Fanout) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s: the fan-out\n\n| Viewers | Seconds | Segments × rungs | Home PUTs | Edge PUTs | Home bytes | R2 reads | R2 max / object / location | DO gets | Viewer segments | Viewer errors | Fetch p95 | After home p95 |\n|---|---|---|---|---|---|---|---|---|---|---|---|---|\n", r.Scenario)
		for _, f := range r.Fanout {
			fmt.Fprintf(&b, "| %d | %d | %d × %d | %d | %d | %d | %d | %d | %d | %d | %d | %.0f ms | %.0f ms |\n",
				f.Viewers, f.Seconds, f.Segments, f.Rungs, f.HomePuts, f.EdgePuts, f.HomeBytes, f.R2Reads, f.R2MaxPerLoc,
				f.DOGets, f.ViewerSegs, f.ViewerErrors, f.FetchP95, f.AfterHomeP95)
		}
	}
	for _, r := range results {
		if len(r.Latency) == 0 {
			continue
		}
		raw, _ := json.Marshal(r.Latency)
		fmt.Fprintf(&b, "\n%s latency (ms): `%s`\n", r.Scenario, raw)
	}
	return b.String()
}

func cell(s string) string {
	if s == "" {
		return "–"
	}
	return strings.ReplaceAll(s, "|", "/")
}

// Latency is a fleet summary's percentiles for the report.
func Latency(s viewers.Summary) map[string]any {
	return map[string]any{"fetch": s.Fetch, "after_home": s.AfterHome}
}
