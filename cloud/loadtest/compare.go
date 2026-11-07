package loadtest

import "fmt"

// Baseline is what CI's load-small measured (story 50.14 §3): the relay's CPU per Mbit/s
// relayed and its memory per tunnel. CI can't measure a Hetzner machine's network, so it checks
// these for regressions against main's last run; the staging runs give the absolute numbers.
//
// CPUSecondsPerMbit is the median of the rounds' (CPURounds), each the relay containers' own
// CPU time (their cgroups' counters) over the Mbit relayed in that round: one long sampled
// figure swung 1.8 times on the same code (#808).
type Baseline struct {
	Sha               string    `json:"sha"`
	Method            string    `json:"method,omitempty"`
	CPUSecondsPerMbit float64   `json:"cpu_seconds_per_mbit"`
	CPURounds         []float64 `json:"cpu_rounds,omitempty"`
	MemBytesPerTunnel float64   `json:"mem_bytes_per_tunnel"`
	Mbit              float64   `json:"mbit"`
	Tunnels           int       `json:"tunnels"`
}

// Method is how CPUSecondsPerMbit is measured now: the median of rounds of the cgroup's CPU
// time. A baseline measured another way is not compared for CPU (its memory still is).
const Method = "cgroup-median-v1"

// Regressed is the tolerance: 20 % worse fails.
const Regressed = 0.20

// Compare says whether cur is more than tol worse than base on either number, and why.
func Compare(base, cur Baseline, tol float64) (ok bool, report string) {
	ok = true
	line := func(name string, b, c float64) string {
		change := 0.0
		if b > 0 {
			change = (c - b) / b
		}
		mark := "ok"
		if b > 0 && change > tol {
			mark, ok = "WORSE", false
		}
		return fmt.Sprintf("%-28s main %.4g  this %.4g  (%+.1f%%)  %s\n", name, b, c, change*100, mark)
	}
	if base.Method == cur.Method {
		report = line("CPU seconds per Mbit relayed", base.CPUSecondsPerMbit, cur.CPUSecondsPerMbit)
	} else {
		report = fmt.Sprintf("%-28s main measured %q, this %q: not compared (main's next run is the baseline)\n",
			"CPU seconds per Mbit relayed", base.Method, cur.Method)
	}
	report += line("memory bytes per tunnel", base.MemBytesPerTunnel, cur.MemBytesPerTunnel)
	if !ok {
		report += fmt.Sprintf("more than %.0f%% worse than main (%s)\n", tol*100, base.Sha)
	}
	return ok, report
}
