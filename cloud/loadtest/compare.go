package loadtest

import "fmt"

// Baseline is what CI's load-small measured (story 50.14 §3): the relay's CPU per Mbit/s
// relayed and its memory per tunnel. CI can't measure a Hetzner machine's network, so it checks
// these for regressions: the base commit's relay and this commit's run in the same job, on the
// same runner, and are compared (a figure from another run is another machine: the same code
// measured 0.00045 to 0.00087 across runners, #832); the staging runs give the absolute numbers.
//
// CPUSecondsPerMbit is the median of the rounds' (CPURounds), each the relay containers' own
// CPU time (their cgroups' counters) over the Mbit relayed in that round: one long sampled
// figure swung 1.8 times on the same code and runner (#808).
//
// HeapBytesPerTunnel is the relays' Go live heap under the load (maktaba_go_heap_live_bytes on
// their /metrics, sampled every 5 s; the median of each round's median, HeapRounds) less their
// live heap before the homes connected, over the tunnels. MemBytesPerTunnel is the containers'
// peak memory from docker stats over the tunnels: one sample of a sawtooth that rides on the
// garbage between collections, it read 254k to 327k on the same code and +28 % on a relay
// that held 70 B more per tunnel, so it is reported and gated only when a side has no heap
// figure (a file from before MemMethod).
type Baseline struct {
	Sha                string    `json:"sha"`
	Method             string    `json:"method,omitempty"`
	CPUSecondsPerMbit  float64   `json:"cpu_seconds_per_mbit"`
	CPURounds          []float64 `json:"cpu_rounds,omitempty"`
	MemBytesPerTunnel  float64   `json:"mem_bytes_per_tunnel"`
	MemMethod          string    `json:"mem_method,omitempty"`
	HeapBytesPerTunnel float64   `json:"heap_bytes_per_tunnel,omitempty"`
	HeapRounds         []float64 `json:"heap_rounds,omitempty"`
	Mbit               float64   `json:"mbit"`
	Tunnels            int       `json:"tunnels"`
}

// Method is how CPUSecondsPerMbit is measured now: the median of rounds of the cgroup's CPU
// time. A baseline measured another way is not compared for CPU (its memory still is).
const Method = "cgroup-median-v1"

// MemMethod is how HeapBytesPerTunnel is measured now. A side measured another way, or whose
// relay served no live heap (a base from before the series), has no heap figure.
const MemMethod = "go-live-heap-median-v1"

// heap is b's heap figure, if it has one measured as MemMethod.
func (b Baseline) heap() (float64, bool) {
	return b.HeapBytesPerTunnel, b.MemMethod == MemMethod && b.HeapBytesPerTunnel > 0
}

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
		report = fmt.Sprintf("%-28s the base measured %q, this %q: not compared\n",
			"CPU seconds per Mbit relayed", base.Method, cur.Method)
	}
	bh, baseHas := base.heap()
	ch, curHas := cur.heap()
	switch {
	case baseHas && curHas:
		report += line("live heap bytes per tunnel", bh, ch)
		report += fmt.Sprintf("%-28s main %.4g  this %.4g  not gated (one peak sample; the live heap is)\n",
			"container peak per tunnel", base.MemBytesPerTunnel, cur.MemBytesPerTunnel)
	case baseHas:
		// a relay that stops serving its live heap must not switch the gate off
		ok = false
		report += fmt.Sprintf("%-28s main %.4g  this: none  WORSE (this relay serves no %s)\n",
			"live heap bytes per tunnel", bh, LiveHeapSeries)
	case curHas:
		// the base's relay predates the live heap: the one pull request that adds it, and those
		// whose base is older; the peak is too noisy to gate on (it is why the heap is read)
		report += fmt.Sprintf("%-28s main: none  this %.4g  not compared (the base's relay serves no live heap)\n",
			"live heap bytes per tunnel", ch)
		report += fmt.Sprintf("%-28s main %.4g  this %.4g  not gated (one peak sample)\n",
			"container peak per tunnel", base.MemBytesPerTunnel, cur.MemBytesPerTunnel)
	default:
		report += line("memory bytes per tunnel", base.MemBytesPerTunnel, cur.MemBytesPerTunnel)
	}
	if !ok {
		report += fmt.Sprintf("more than %.0f%% worse than main (%s)\n", tol*100, base.Sha)
	}
	return ok, report
}
