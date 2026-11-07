package loadtest

import (
	"fmt"
	"strings"
	"time"
)

// Step is one step of a ramp and what it carried.
type Step struct {
	Homes       int     `json:"homes"`
	Players     int     `json:"players"`
	Mbit        float64 `json:"mbit_per_second"`
	CPU         float64 `json:"cpu"`
	Mem         float64 `json:"mem"`
	StallRate   float64 `json:"stall_rate"`
	AddedP95MS  float64 `json:"added_p95_ms"`
	BitsPerSec  int     `json:"bits_per_second"`
	StoppedBy   string  `json:"stopped_by,omitempty"`
	Seconds     int     `json:"seconds"`
	Concurrency int     `json:"-"`
}

// Report is a ramp's outcome: the last step that broke no rule is the node's capacity.
type Report struct {
	Target   string    `json:"target"`
	Plan     string    `json:"plan"`
	Machine  string    `json:"machine"` // vCPU / RAM
	Sha      string    `json:"sha"`
	At       time.Time `json:"at"`
	Steps    []Step    `json:"steps"`
	LastGood *Step     `json:"last_good"`
	Stopped  string    `json:"stopped_by"`
}

// SizingRow is the report's row of docs/cloud-sizing.md (story 50.14 §4).
func SizingRow(r Report) string {
	g := Step{}
	if r.LastGood != nil {
		g = *r.LastGood
	}
	streams := 0
	if g.BitsPerSec > 0 {
		streams = int(g.Mbit * 1e6 / float64(g.BitsPerSec))
	}
	cpuPer100 := 0.0
	if g.Mbit > 0 {
		cpuPer100 = g.CPU * 100 / g.Mbit * 100
	}
	return fmt.Sprintf("| %s | %s | %d | %.0f | %d | %.1f%% | %s, %s |",
		r.Plan, r.Machine, g.Homes, g.Mbit, streams, cpuPer100, r.At.UTC().Format("2006-01-02"), shortSha(r.Sha))
}

func shortSha(s string) string {
	if len(s) > 9 {
		return s[:9]
	}
	return s
}

// UpdateSizing puts row in doc's table: in place of the plan's row, or added after the last.
func UpdateSizing(doc, plan, row string) (string, error) {
	lines := strings.Split(doc, "\n")
	last := -1
	for i, l := range lines {
		if !strings.HasPrefix(l, "| ") || strings.HasPrefix(l, "| Plan ") {
			continue
		}
		if strings.HasPrefix(l, "|---") {
			continue
		}
		cells := strings.Split(l, "|")
		if len(cells) > 1 && strings.TrimSpace(cells[1]) == plan {
			lines[i] = row
			return strings.Join(lines, "\n"), nil
		}
		last = i
	}
	if last < 0 {
		for i, l := range lines {
			if strings.HasPrefix(l, "|---") {
				last = i
			}
		}
	}
	if last < 0 {
		return "", fmt.Errorf("no sizing table in the document")
	}
	lines = append(lines[:last+1], append([]string{row}, lines[last+1:]...)...)
	return strings.Join(lines, "\n"), nil
}
