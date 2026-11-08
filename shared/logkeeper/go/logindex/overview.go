package logindex

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"
	"strings"
)

// The Overview with many machines (story 53.12). Every joined machine adds its load, memory,
// disk free and a line per component (labelled machine=<node id>), so a line per machine
// does not fit a board's MaxSeries once there are more than a few machines. With no machine
// asked for, the Overview answers:
//
//   - totals across all machines (when there are two or more): components up and down,
//     the lowest disk free and memory free, the highest load, the restarts per minute;
//   - for the TopMachines machines most in need of attention now (most components down,
//     then least disk free, then the highest load per CPU, by their latest values), their
//     components down, load, disk free and memory free;
//   - in Machines, how many machines the store has lines of, which are shown, and every
//     machine's id, so a client can say "showing 5 of 37" and offer each machine on its own
//     (machine=).
//
// With machine=<id>, it answers that machine's lines only: its components up and down, its
// load, CPUs, memory and disk, and a line for each component that was not up all the time,
// and each that restarted.
//
// What the board reads stays small however many machines and components there are: Record
// sums each machine's components up, components and restarts (maktaba_machine_*{machine})
// and the totals across machines (maktaba_machines_*) as it writes a scrape; the ranking
// reads each machine's latest values through the primary key; and only the shown machines'
// lines are read in full. (Summing 50 machines' 45 components over 30 days when the board
// was asked took seconds, past the 500 ms budget.)

// TopMachines is how many machines the Overview draws a line each for.
const TopMachines = 5

// maxMachineID bounds the machine= parameter (a node id is far shorter).
const maxMachineID = 128

// MachineCount is the Overview's machines: how many the store has lines of, the ones shown
// (most in need of attention first), and all of them (sorted), for a machine picker.
type MachineCount struct {
	Total int      `json:"total"`
	Shown []string `json:"shown"`
	All   []string `json:"all"`
}

// The totals across machines Record stores with each scrape (no labels).
const (
	MachinesComponentsUp   = "maktaba_machines_components_up"
	MachinesComponentsDown = "maktaba_machines_components_down"
	MachinesDiskFreeMin    = "maktaba_machines_disk_free_min_bytes"
	MachinesMemoryFreeMin  = "maktaba_machines_memory_free_min_bytes"
	MachinesLoadMax        = "maktaba_machines_load1_max"
	MachinesRestarts       = "maktaba_machines_restarts"
)

// The per-machine sums Record stores with each scrape, labelled machine=<id>.
const (
	MachineComponentsUp = "maktaba_machine_components_up" // components up (a gauge)
	MachineComponents   = "maktaba_machine_components"    // components it runs (a gauge)
	MachineRestarts     = "maktaba_machine_restarts"      // restarts (a counter: per minute)
	// ComponentDown is one machine's components down, drawn from the two above.
	ComponentDown = "maktaba_component_down"
)

const (
	hostLoad          = "maktaba_host_load1"
	hostCPUs          = "maktaba_host_cpus"
	hostMemory        = "maktaba_host_memory_bytes"
	hostMemoryFree    = "maktaba_host_memory_free_bytes"
	hostDiskFree      = "maktaba_host_disk_free_bytes"
	componentUp       = "maktaba_component_up"
	componentRestarts = "maktaba_component_restarts_total"
)

// machineSeries is a series with a line per machine (the supervisor's host and component
// series, which each joined machine sends too).
func machineSeries(name string) bool {
	return strings.HasPrefix(name, "maktaba_host_") || strings.HasPrefix(name, "maktaba_component_")
}

// ---- written with each scrape ----

// machineSums is one machine's sums in one scrape.
type machineSums struct {
	up, components, restarts float64
	hasRestarts              bool
}

// machineTally sums one scrape's kept per-machine samples (Record).
type machineTally struct {
	per                      map[string]*machineSums
	disk, mem, load          float64
	hasDisk, hasMem, hasLoad bool
}

type derivedRow struct {
	name, labels string
	v            float64
	add          bool // a counter's increase: added to the minute's rate
}

func newMachineTally() *machineTally { return &machineTally{per: map[string]*machineSums{}} }

// add takes one kept sample as it is stored: a gauge's value, a counter's increase.
func (t *machineTally) add(name, labels string, v float64) {
	m := labelValue(labels, "machine")
	if m == "" {
		return
	}
	sums := func() *machineSums {
		if t.per[m] == nil {
			t.per[m] = &machineSums{}
		}
		return t.per[m]
	}
	switch name {
	case componentUp:
		ms := sums()
		ms.up, ms.components = ms.up+v, ms.components+1
	case componentRestarts:
		ms := sums()
		ms.restarts, ms.hasRestarts = ms.restarts+v, true
	case hostDiskFree:
		if !t.hasDisk || v < t.disk {
			t.disk, t.hasDisk = v, true
		}
	case hostMemoryFree:
		if !t.hasMem || v < t.mem {
			t.mem, t.hasMem = v, true
		}
	case hostLoad:
		if !t.hasLoad || v > t.load {
			t.load, t.hasLoad = v, true
		}
	}
}

// rows are the sums to store: each machine's, then the totals across machines.
func (t *machineTally) rows() []derivedRow {
	var out []derivedRow
	var up, down, restarts float64
	var hasComponents, hasRestarts bool
	for m, ms := range t.per {
		l := "machine=" + m
		if ms.components > 0 {
			out = append(out, derivedRow{MachineComponentsUp, l, ms.up, false},
				derivedRow{MachineComponents, l, ms.components, false})
			up, down, hasComponents = up+ms.up, down+math.Max(0, ms.components-ms.up), true
		}
		if ms.hasRestarts {
			out = append(out, derivedRow{MachineRestarts, l, ms.restarts, true})
			restarts, hasRestarts = restarts+ms.restarts, true
		}
	}
	if hasComponents {
		out = append(out, derivedRow{MachinesComponentsUp, "", up, false}, derivedRow{MachinesComponentsDown, "", down, false})
	}
	if hasRestarts {
		out = append(out, derivedRow{MachinesRestarts, "", restarts, true})
	}
	if t.hasDisk {
		out = append(out, derivedRow{MachinesDiskFreeMin, "", t.disk, false})
	}
	if t.hasMem {
		out = append(out, derivedRow{MachinesMemoryFreeMin, "", t.mem, false})
	}
	if t.hasLoad {
		out = append(out, derivedRow{MachinesLoadMax, "", t.load, false})
	}
	return out
}

// ---- read by the board ----

// nextLabels is a series' first stored label set at or after `from` (after it when
// inclusive is false), found through the primary key (name, labels, …).
func (ix *Index) nextLabels(ctx context.Context, table, name, from string, inclusive bool) (string, bool, error) {
	op := ">"
	if inclusive {
		op = ">="
	}
	var l string
	err := ix.db.QueryRowContext(ctx, `SELECT labels FROM `+table+` WHERE name = ? AND labels `+op+` ?
		ORDER BY labels LIMIT 1`, name, from).Scan(&l)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return l, err == nil, err
}

// machinesOf are the machines a per-machine series (labels machine=<id>) has lines of.
func (ix *Index) machinesOf(ctx context.Context, table, name string, into map[string]bool) error {
	from := ""
	for {
		l, ok, err := ix.nextLabels(ctx, table, name, from, false)
		if err != nil || !ok {
			return err
		}
		if m := labelValue(l, "machine"); m != "" {
			into[m] = true
		}
		from = l
	}
}

// componentSets are one machine's label sets of an id series. The labels are sorted
// ("id=…,machine=…[,phase=…]"), so it skips through the key: from a component's line of an
// earlier machine to this machine's, and from a later machine's to the next component.
func (ix *Index) componentSets(ctx context.Context, table, name, machine string) ([]string, error) {
	var out []string
	from, inclusive := "", false
	for steps := 0; steps < 100_000; steps++ {
		l, ok, err := ix.nextLabels(ctx, table, name, from, inclusive)
		if err != nil || !ok {
			return out, err
		}
		id := labelValue(l, "id")
		prefix := "id=" + id + ",machine="
		m := labelValue(l, "machine")
		switch {
		case !strings.HasPrefix(l, prefix): // not the shape of a component's line: step past it
			from, inclusive = l, false
		case m == machine:
			out = append(out, l)
			from, inclusive = l, false
		case m < machine:
			from, inclusive = prefix+machine, true
		default: // past this machine: the next component ('-' sorts just after ',')
			from, inclusive = "id="+id+"-", true
		}
	}
	return out, nil
}

// latest is a per-machine line's newest value (the minutes first, then the hours).
func (ix *Index) latest(ctx context.Context, name, labels string) (float64, bool) {
	for _, q := range []string{
		`SELECT value FROM metric_1m WHERE name = ? AND labels = ? ORDER BY minute DESC LIMIT 1`,
		`SELECT value FROM metric_1h WHERE name = ? AND labels = ? ORDER BY hour DESC LIMIT 1`,
	} {
		var v float64
		if ix.db.QueryRowContext(ctx, q, name, labels).Scan(&v) == nil {
			return v, true
		}
	}
	return 0, false
}

// line is one stored line over the window (nil points when it has none).
func (ix *Index) line(ctx context.Context, w window, name, kind, labels string) (Series, error) {
	ss, err := ix.seriesOf(ctx, w, name, kind, labels)
	if err != nil || len(ss) == 0 {
		return Series{Name: name, Labels: labels, Kind: kind}, err
	}
	return ss[0], nil
}

// downLine is a machine's components down over the window: its components less those up.
func (ix *Index) downLine(ctx context.Context, w window, machine string) (up, down Series, err error) {
	l := "machine=" + machine
	if up, err = ix.line(ctx, w, MachineComponentsUp, "gauge", l); err != nil {
		return
	}
	n, err := ix.line(ctx, w, MachineComponents, "gauge", l)
	if err != nil {
		return
	}
	count := map[float64]float64{}
	for _, p := range n.Points {
		count[p[0]] = p[1]
	}
	down = Series{Name: ComponentDown, Labels: l, Kind: "gauge"}
	for _, p := range up.Points {
		if c, ok := count[p[0]]; ok {
			down.Points = append(down.Points, [2]float64{p[0], math.Max(0, c-p[1])})
		}
	}
	up.Name = componentUp
	return
}

// perComponent is one machine's lines of an id series, a line per component (a component
// whose phase changed is one line: its label sets' values averaged at each time), keeping
// only the lines `keep` wants.
func (ix *Index) perComponent(ctx context.Context, w window, name, kind, machine string,
	keep func(Series) bool) ([]Series, error) {
	sets, err := ix.componentSets(ctx, w.table, name, machine)
	if err != nil {
		return nil, err
	}
	type acc struct{ sum, n float64 }
	byKey := map[string]map[float64]*acc{}
	var keys []string
	for _, l := range sets {
		var kv []string
		for _, p := range strings.Split(l, ",") {
			if !strings.HasPrefix(p, "phase=") {
				kv = append(kv, p)
			}
		}
		key := strings.Join(kv, ",")
		s, err := ix.line(ctx, w, name, kind, l)
		if err != nil {
			return nil, err
		}
		if byKey[key] == nil {
			byKey[key] = map[float64]*acc{}
			keys = append(keys, key)
		}
		for _, p := range s.Points {
			a := byKey[key][p[0]]
			if a == nil {
				a = &acc{}
				byKey[key][p[0]] = a
			}
			a.sum, a.n = a.sum+p[1], a.n+1
		}
	}
	sort.Strings(keys)
	var out []Series
	for _, k := range keys {
		var ts []float64
		for t := range byKey[k] {
			ts = append(ts, t)
		}
		sort.Float64s(ts)
		s := Series{Name: name, Labels: k, Kind: kind}
		for _, t := range ts {
			a := byKey[k][t]
			s.Points = append(s.Points, [2]float64{t, a.sum / a.n})
		}
		if keep(s) {
			out = append(out, s)
		}
	}
	return out, nil
}

// overviewMachines answers the Overview's per-machine lines (see the top of this file).
func (ix *Index) overviewMachines(ctx context.Context, w window, machine string) ([]Series, *MachineCount, error) {
	seen := map[string]bool{}
	for _, name := range []string{MachineComponents, MachineRestarts, hostLoad, hostDiskFree, hostMemoryFree} {
		if err := ix.machinesOf(ctx, w.table, name, seen); err != nil {
			return nil, nil, err
		}
	}
	all := make([]string, 0, len(seen))
	for m := range seen {
		all = append(all, m)
	}
	sort.Strings(all)
	count := &MachineCount{Total: len(all), Shown: []string{}, All: all}
	var out []Series
	hostLines := func(m string, names ...string) error {
		for _, name := range names {
			s, err := ix.line(ctx, w, name, "gauge", "machine="+m)
			if err != nil {
				return err
			}
			if len(s.Points) > 0 {
				out = append(out, s)
			}
		}
		return nil
	}

	if machine != "" {
		if !seen[machine] {
			return []Series{}, count, nil
		}
		count.Shown = []string{machine}
		up, down, err := ix.downLine(ctx, w, machine)
		if err != nil {
			return nil, nil, err
		}
		if len(up.Points) > 0 {
			out = append(out, up, down)
		}
		if err := hostLines(machine, hostLoad, hostCPUs, hostMemory, hostMemoryFree, hostDiskFree); err != nil {
			return nil, nil, err
		}
		// each component that was not up all the time, then each that restarted
		for _, c := range []struct {
			name, kind string
			keep       func(Series) bool
		}{
			{componentUp, "gauge", func(s Series) bool { return anyPoint(s, func(v float64) bool { return v < 1 }) }},
			{componentRestarts, "counter", func(s Series) bool { return anyPoint(s, func(v float64) bool { return v > 0 }) }},
		} {
			ss, err := ix.perComponent(ctx, w, c.name, c.kind, machine, c.keep)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, ss...)
		}
		return out, count, nil
	}

	// rank the machines by their latest values: most components down, then least disk free,
	// then the highest load per CPU
	type rank struct {
		m                string
		down, disk, load float64
	}
	ranks := make([]rank, 0, len(all))
	for _, m := range all {
		l := "machine=" + m
		r := rank{m: m, disk: math.Inf(1)}
		if n, ok := ix.latest(ctx, MachineComponents, l); ok {
			u, _ := ix.latest(ctx, MachineComponentsUp, l)
			r.down = math.Max(0, n-u)
		}
		if v, ok := ix.latest(ctx, hostDiskFree, l); ok {
			r.disk = v
		}
		r.load, _ = ix.latest(ctx, hostLoad, l)
		if c, ok := ix.latest(ctx, hostCPUs, l); ok && c > 0 {
			r.load /= c
		}
		ranks = append(ranks, r)
	}
	sort.SliceStable(ranks, func(i, j int) bool {
		a, b := ranks[i], ranks[j]
		if a.down != b.down {
			return a.down > b.down
		}
		if a.disk != b.disk {
			return a.disk < b.disk
		}
		if a.load != b.load {
			return a.load > b.load
		}
		return a.m < b.m
	})
	for i := 0; i < len(ranks) && i < TopMachines; i++ {
		count.Shown = append(count.Shown, ranks[i].m)
	}

	if len(all) > 1 { // totals across the machines (one machine's own lines say the same)
		for _, t := range []struct{ name, kind string }{
			{MachinesComponentsUp, "gauge"}, {MachinesComponentsDown, "gauge"}, {MachinesDiskFreeMin, "gauge"},
			{MachinesMemoryFreeMin, "gauge"}, {MachinesLoadMax, "gauge"}, {MachinesRestarts, "counter"},
		} {
			s, err := ix.line(ctx, w, t.name, t.kind, "")
			if err != nil {
				return nil, nil, err
			}
			if len(s.Points) > 0 {
				out = append(out, s)
			}
		}
	}
	// the shown machines' lines, one panel per name, the machines in their rank's order
	for _, m := range count.Shown {
		_, down, err := ix.downLine(ctx, w, m)
		if err != nil {
			return nil, nil, err
		}
		if len(down.Points) > 0 {
			out = append(out, down)
		}
	}
	for _, name := range []string{hostLoad, hostDiskFree, hostMemoryFree} {
		for _, m := range count.Shown {
			if err := hostLines(m, name); err != nil {
				return nil, nil, err
			}
		}
	}
	return out, count, nil
}

func anyPoint(s Series, f func(float64) bool) bool {
	for _, p := range s.Points {
		if f(p[1]) {
			return true
		}
	}
	return false
}
