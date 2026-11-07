package loadtest

import (
	"fmt"
	"time"
)

// Sample is one look at the node and the players (every 5 s).
type Sample struct {
	At        time.Time
	CPU       float64       // the relay's share of the machine (0–1)
	Mem       float64       // memory in use, share of the machine (0–1)
	AddedP95  time.Duration // p95 time to first byte through the relay, minus straight to the home
	StallRate float64       // share of segments that stalled or failed
	Dropped   int           // tunnels dropped that weren't drained
}

// Rules say what "full" means (story 50.14 §2): a step fails when any holds for Hold.
type Rules struct {
	CPU      float64
	Mem      float64
	AddedP95 time.Duration
	Stall    float64
	Hold     time.Duration
}

// DefaultRules are the story's: CPU 70 %, memory 75 %, 20 ms added, 0.5 % stalls, any drop,
// each for 2 minutes.
var DefaultRules = Rules{CPU: 0.70, Mem: 0.75, AddedP95: 20 * time.Millisecond, Stall: 0.005, Hold: 2 * time.Minute}

// broken names the rules s breaks.
func (r Rules) broken(s Sample) []string {
	var out []string
	if s.CPU > r.CPU {
		out = append(out, fmt.Sprintf("cpu %.0f%% > %.0f%%", s.CPU*100, r.CPU*100))
	}
	if s.Mem > r.Mem {
		out = append(out, fmt.Sprintf("memory %.0f%% > %.0f%%", s.Mem*100, r.Mem*100))
	}
	if s.AddedP95 > r.AddedP95 {
		out = append(out, fmt.Sprintf("p95 added latency %s > %s", s.AddedP95, r.AddedP95))
	}
	if s.StallRate > r.Stall {
		out = append(out, fmt.Sprintf("stalls %.2f%% > %.2f%%", s.StallRate*100, r.Stall*100))
	}
	if s.Dropped > 0 {
		out = append(out, fmt.Sprintf("%d tunnels dropped", s.Dropped))
	}
	return out
}

// Watch follows a step's samples: a rule fails the step once it has held, sample after
// sample, for the rules' Hold.
type Watch struct {
	Rules Rules
	since map[string]time.Time // a rule's name (its kind) -> when it began to hold
}

func kind(b string) string {
	for i, c := range b {
		if c == ' ' {
			return b[:i]
		}
	}
	return b
}

// Add takes the next sample; it returns the rule that has now held for Hold, or "".
func (w *Watch) Add(s Sample) string {
	if w.since == nil {
		w.since = map[string]time.Time{}
	}
	now := map[string]string{}
	for _, b := range w.Rules.broken(s) {
		now[kind(b)] = b
	}
	for k := range w.since {
		if _, still := now[k]; !still {
			delete(w.since, k) // it let go: the clock starts again next time
		}
	}
	for k, b := range now {
		first, ok := w.since[k]
		if !ok {
			w.since[k] = s.At
			first = s.At
		}
		if s.At.Sub(first) >= w.Rules.Hold {
			return b
		}
	}
	return ""
}
