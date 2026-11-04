package supervisor

import (
	"context"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.14: the components of this machine on the supervisor's /metrics, beside the
// update metrics (serve_metrics.go). The rows are this machine's component_status, read
// through the store at most once per ComponentMetricsTTL however often Prometheus
// scrapes. Labels carry only catalog ids, phases and the machine ("home" or the node's
// id): never user data.
//
//	maktaba_component_up{id,machine,phase}            1 running or degraded, else 0
//	maktaba_component_restarts_total{id,machine}      the supervisor's restarts of its processes
//	maktaba_component_transition_seconds{id,to}       from the generation a runner acted on
//	                                                  to the phase it reached (detail.last_transition)
//
// A component whose desired state is absent is not exported. The histogram counts each
// transition a runner reports once (in this process's memory: it starts again at 0 when
// the supervisor restarts, as a Prometheus counter may).

// Metric names.
const (
	MetricComponentUp         = "maktaba_component_up"
	MetricComponentRestarts   = "maktaba_component_restarts_total"
	MetricComponentTransition = "maktaba_component_transition_seconds"
	// ComponentMetricsTTL is how long one read of the rows serves the scrapes.
	ComponentMetricsTTL = 5 * time.Second
)

// TransitionBuckets are the histogram's upper bounds, in seconds.
var TransitionBuckets = []float64{0.5, 1, 2, 5, 10, 15, 30, 60, 120}

// ComponentRows reads one machine's rows and desired state (*store.Store).
type ComponentRows interface {
	StatusesOfMachine(ctx context.Context, machine string) ([]store.Status, error)
	Desired(ctx context.Context, machine string) (map[string]store.Desired, int64, error)
}

// ComponentMetrics writes this machine's component metrics.
type ComponentMetrics struct {
	Source  ComponentRows
	Machine string // store.Home or the node's id
	Catalog *components.Catalog
	Now     func() time.Time

	mu      sync.Mutex
	readAt  time.Time
	rows    []store.Status
	desired map[string]store.Desired
	readErr error
	hist    map[transitionKey]*transitionHist
	seen    map[string]string // id/part -> the last transition counted
}

type transitionKey struct{ id, to string }

type transitionHist struct {
	buckets []uint64
	count   uint64
	sum     float64
}

func (m *ComponentMetrics) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// read refreshes the rows when the last read is older than the TTL (a failed read is
// kept for the TTL too: a database that is down is not asked on every scrape).
func (m *ComponentMetrics) read(ctx context.Context) {
	now := m.now()
	if !m.readAt.IsZero() && now.Sub(m.readAt) < ComponentMetricsTTL {
		return
	}
	m.readAt = now
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := m.Source.StatusesOfMachine(ctx, m.Machine)
	if err != nil {
		m.readErr = err
		return
	}
	desired, _, err := m.Source.Desired(ctx, m.Machine)
	if err != nil {
		m.readErr = err
		return
	}
	m.rows, m.desired, m.readErr = rows, desired, nil
	m.count(rows)
}

// detailNum is a number of a detail map: float64 once read back from the database, an
// int when built in Go.
func detailNum(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// count adds each transition not counted yet to the histogram.
func (m *ComponentMetrics) count(rows []store.Status) {
	if m.hist == nil {
		m.hist, m.seen = map[transitionKey]*transitionHist{}, map[string]string{}
	}
	for _, r := range rows {
		lt, ok := r.Detail["last_transition"].(map[string]any)
		if !ok {
			continue
		}
		to, _ := lt["to"].(string)
		secs, ok := detailNum(lt["seconds"])
		if !ok || to == "" || !slices.Contains(components.KnownPhases, to) || secs < 0 || math.IsNaN(secs) || math.IsInf(secs, 0) {
			continue
		}
		mark := fmt.Sprintf("%s|%v|%d", to, secs, r.ObservedGeneration)
		k := r.ID + "/" + r.Part
		if m.seen[k] == mark {
			continue
		}
		m.seen[k] = mark
		h := m.hist[transitionKey{r.ID, to}]
		if h == nil {
			h = &transitionHist{buckets: make([]uint64, len(TransitionBuckets))}
			m.hist[transitionKey{r.ID, to}] = h
		}
		for i, b := range TransitionBuckets {
			if secs <= b {
				h.buckets[i]++
			}
		}
		h.count++
		h.sum += secs
	}
}

// Write writes the metrics in Prometheus's text format. A failed read writes the
// families with no samples (the update metrics beside it still go out).
func (m *ComponentMetrics) Write(ctx context.Context, w io.Writer) error {
	if m == nil || m.Source == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.read(ctx)
	cat := m.Catalog
	if cat == nil {
		cat = components.Default()
	}
	machine := m.Machine
	if machine == "" {
		machine = store.Home
	}
	byID := map[string][]store.Status{}
	for _, r := range m.rows {
		if _, ok := cat.Get(r.ID); !ok {
			continue // an id this release does not know: never a label
		}
		if d, ok := m.desired[r.ID]; ok && d.Want == components.WantAbsent {
			continue
		}
		byID[r.ID] = append(byID[r.ID], r)
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var b strings.Builder
	fmt.Fprintf(&b, "# HELP %s Whether a component of this machine runs (1 running or degraded); phase tells off from failed.\n# TYPE %s gauge\n",
		MetricComponentUp, MetricComponentUp)
	for _, id := range ids {
		roll := store.Rollup(byID[id])
		up := 0
		if roll.Phase == components.PhaseRunning || roll.Phase == components.PhaseDegraded {
			up = 1
		}
		phase := roll.Phase
		if !slices.Contains(components.KnownPhases, phase) {
			phase = "unknown"
		}
		fmt.Fprintf(&b, "%s{id=%q,machine=%q,phase=%q} %d\n", MetricComponentUp, id, machine, phase, up)
	}
	fmt.Fprintf(&b, "# HELP %s How often the supervisor restarted a component's processes.\n# TYPE %s counter\n",
		MetricComponentRestarts, MetricComponentRestarts)
	for _, id := range ids {
		n := 0
		for _, r := range byID[id] {
			if v, ok := detailNum(r.Detail["restarts"]); ok && v > 0 {
				n += int(v)
			}
		}
		fmt.Fprintf(&b, "%s{id=%q,machine=%q} %d\n", MetricComponentRestarts, id, machine, n)
	}
	fmt.Fprintf(&b, "# HELP %s From the generation a runner acted on to the phase it reached.\n# TYPE %s histogram\n",
		MetricComponentTransition, MetricComponentTransition)
	keys := make([]transitionKey, 0, len(m.hist))
	for k := range m.hist {
		if _, ok := cat.Get(k.id); ok {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].id != keys[j].id {
			return keys[i].id < keys[j].id
		}
		return keys[i].to < keys[j].to
	})
	n := MetricComponentTransition
	for _, k := range keys {
		h := m.hist[k]
		for i, le := range TransitionBuckets {
			fmt.Fprintf(&b, "%s_bucket{id=%q,to=%q,le=%q} %d\n", n, k.id, k.to, strconv.FormatFloat(le, 'g', -1, 64), h.buckets[i])
		}
		fmt.Fprintf(&b, "%s_bucket{id=%q,to=%q,le=\"+Inf\"} %d\n%s_sum{id=%q,to=%q} %s\n%s_count{id=%q,to=%q} %d\n",
			n, k.id, k.to, h.count, n, k.id, k.to, strconv.FormatFloat(h.sum, 'g', -1, 64), n, k.id, k.to, h.count)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
