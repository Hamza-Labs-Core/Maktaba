// Package hostload says when the machine this server runs on is overloaded: more work
// wants the CPU than it has cores, for minutes, so every job and the database are slow.
// The cause is often a program outside Maktaba, and nothing in Maktaba fails: the queues
// only crawl. The Queues page and System health show it (the pulse's `host` block, the
// host_load item), with the programs that use the most CPU.
//
// The rule: overloaded when the 1-minute and the 5-minute load averages are both above
// OverloadFactor times the logical cores. Both, so a short burst (a scan starting, a
// build) does not raise it, and it clears within a minute or so of the load going. On
// Linux the load average also counts work waiting on a disk, so a stuck share can raise
// it with idle CPUs. Windows has no load average: nothing is reported there.
//
// It reads the load of this machine only (where the API runs: the home). It ends no
// process: it explains.
package hostload

import (
	"context"
	"runtime"
	"sync"
	"time"
)

// OverloadFactor is how many times the cores the load must pass.
const OverloadFactor = 2.0

// TopCount is how many programs an overloaded state names.
const TopCount = 5

// cacheFor is how long a reading is kept: pages poll every few seconds.
const cacheFor = 15 * time.Second

// Load is the machine's load averages and its logical cores.
type Load struct {
	Load1, Load5, Load15 float64
	Cores                int
}

// Overloaded applies the rule.
func (l Load) Overloaded() bool {
	bound := OverloadFactor * float64(l.Cores)
	return l.Cores > 0 && l.Load1 > bound && l.Load5 > bound
}

// Proc is one program and its share of one core, in percent (400: four cores).
type Proc struct {
	PID    int     `json:"pid"`
	Name   string  `json:"name"`
	CPUPct float64 `json:"cpu_pct"`
}

// State is what the pages show.
type State struct {
	Overloaded bool    `json:"overloaded"`
	Load1      float64 `json:"load1"`
	Load5      float64 `json:"load5"`
	Load15     float64 `json:"load15"`
	Cores      int     `json:"cores"`
	// Bound is the load both averages must pass: OverloadFactor times the cores.
	Bound float64 `json:"bound"`
	// Top is the programs using the most CPU, most first: only while overloaded, and
	// empty when they cannot be listed.
	Top []Proc `json:"top"`
}

// Monitor reads the load, at most once per cacheFor.
type Monitor struct {
	// Read is the load reading (default: this machine's); false: not readable here.
	Read func() (Load, bool)
	// Top lists the n programs using the most CPU (default: ps); nil result: unknown.
	Top func(ctx context.Context, n int) []Proc
	Now func() time.Time

	mu     sync.Mutex
	at     time.Time
	cached State
	ok     bool
}

// New is a Monitor of this machine.
func New() *Monitor { return &Monitor{} }

// State is the current reading (false: the load cannot be read on this machine).
func (m *Monitor) State(ctx context.Context) (State, bool) {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.at.IsZero() && now().Sub(m.at) < cacheFor {
		return m.cached, m.ok
	}
	read, top := m.Read, m.Top
	if read == nil {
		read = ReadLoad
	}
	if top == nil {
		top = TopProcs
	}
	l, ok := read()
	st := State{Top: []Proc{}}
	if ok {
		st = State{Overloaded: l.Overloaded(), Load1: l.Load1, Load5: l.Load5, Load15: l.Load15,
			Cores: l.Cores, Bound: OverloadFactor * float64(l.Cores), Top: []Proc{}}
		if st.Overloaded {
			if ps := top(ctx, TopCount); ps != nil {
				st.Top = ps
			}
		}
	}
	m.at, m.cached, m.ok = now(), st, ok
	return st, ok
}

// ReadLoad is this machine's load and cores (false where there is no load average).
func ReadLoad() (Load, bool) {
	avg, ok := loadAvg()
	if !ok {
		return Load{}, false
	}
	return Load{Load1: avg[0], Load5: avg[1], Load15: avg[2], Cores: runtime.NumCPU()}, true
}
