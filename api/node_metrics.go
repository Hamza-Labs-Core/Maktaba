package main

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

// Epic 53 story 53.12: the home's dashboards show "CPU, memory, disk free of the home and
// each machine" and each machine's components. Each machine's supervisor writes those
// families labelled with its machine (maktaba_host_*, maktaba_component_*), but the home's
// log index scrapes only the home's own endpoints. So the home's /metrics also carries
// every joined machine's: the home asks each machine's own agent over the cluster channel
// (the "metrics" command, as logs.files) and adds the series that carry a machine label.

const (
	// nodeMetricsWait bounds one round of asking: a machine slower than this adds nothing
	// to the scrape, so a slow machine never holds the home's /metrics back.
	nodeMetricsWait = 2 * time.Second
	// nodeMetricsTTL is how long one round serves the scrapes (the log index scrapes every
	// 15 s; a Prometheus beside it does not ask the machines again).
	nodeMetricsTTL = 10 * time.Second
	// nodeMetricsMax is the most of one machine's answer that is read.
	nodeMetricsMax = 1 << 20
)

// machineAsker is the cluster channel as the home's /metrics reaches the machines
// (*agent.Hub).
type machineAsker interface {
	MachineAgents() map[string]string
	CommandWithin(ctx context.Context, sessionID, name string, args json.RawMessage, by string, wait time.Duration) (agent.CmdResult, error)
}

// nodeMetrics gathers the joined machines' per-machine families. hub is nil (or returns
// nil) where there is no channel: a node, a home whose channel is not mounted yet.
type nodeMetrics struct {
	hub  func() machineAsker
	wait time.Duration
	ttl  time.Duration
	now  func() time.Time

	mu   sync.Mutex
	at   time.Time
	fams []*dto.MetricFamily
}

func (n *nodeMetrics) clock() time.Time {
	if n.now != nil {
		return n.now()
	}
	return time.Now()
}

// Families is every connected machine's families that answered within the wait, from
// the last round while it is younger than the TTL.
func (n *nodeMetrics) Families() []*dto.MetricFamily {
	if n == nil || n.hub == nil {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.clock()
	if !n.at.IsZero() && now.Sub(n.at) < n.ttl {
		return n.fams
	}
	n.fams, n.at = n.round(), now
	return n.fams
}

// round asks every machine at once and merges the answers.
func (n *nodeMetrics) round() []*dto.MetricFamily {
	hub := n.hub()
	if hub == nil {
		return nil
	}
	agents := hub.MachineAgents()
	if len(agents) == 0 {
		return nil
	}
	wait := n.wait
	if wait <= 0 {
		wait = nodeMetricsWait
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	type answer struct {
		node string
		fams map[string]*dto.MetricFamily
	}
	ch := make(chan answer, len(agents))
	for node, session := range agents {
		go func() {
			ch <- answer{node, askMachine(ctx, hub, node, session, wait)}
		}()
	}
	// the answers in node order, so the merged families do not change from scrape to scrape
	got := make([]answer, 0, len(agents))
	for range agents {
		got = append(got, <-ch)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].node < got[j].node })
	merged := map[string]*dto.MetricFamily{}
	for _, a := range got {
		mergeFamilies(merged, a.fams)
	}
	return familyList(merged)
}

// askMachine is one machine's per-machine families, each series' machine label set to
// the machine's id (a machine never speaks for the home or another); nil when it does
// not answer, answers no, or answers what is not the text format.
func askMachine(ctx context.Context, hub machineAsker, node, session string, wait time.Duration) map[string]*dto.MetricFamily {
	res, err := hub.CommandWithin(ctx, session, "metrics", nil, "system:metrics", wait)
	if err != nil || !res.OK {
		return nil
	}
	var text string
	if json.Unmarshal(res.Detail, &text) != nil || len(text) > nodeMetricsMax {
		return nil
	}
	p := expfmt.NewTextParser(model.LegacyValidation)
	fams, err := p.TextToMetricFamilies(strings.NewReader(text))
	if err != nil {
		return nil
	}
	out := map[string]*dto.MetricFamily{}
	for name, f := range fams {
		if !strings.HasPrefix(name, "maktaba_") {
			continue
		}
		var kept []*dto.Metric
		for _, m := range f.GetMetric() {
			if setMachine(m, node) {
				kept = append(kept, m)
			}
		}
		if len(kept) > 0 {
			f.Metric = kept
			out[name] = f
		}
	}
	return out
}

// setMachine sets a series' machine label to node; false when it has none (a series
// of the machine's without one would repeat the home's own).
func setMachine(m *dto.Metric, node string) bool {
	for _, l := range m.GetLabel() {
		if l.GetName() == "machine" {
			l.Value = &node
			return true
		}
	}
	return false
}

// labelKey is a series' label set as one key.
func labelKey(m *dto.Metric) string {
	parts := make([]string, 0, len(m.GetLabel()))
	for _, l := range m.GetLabel() {
		parts = append(parts, l.GetName()+"\x00"+l.GetValue())
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01")
}

// mergeFamilies adds src's series to dst: a family dst has keeps its help and type (one
// of another type is left out), and a series whose label set dst already has is left
// out, so no family or series repeats (a repeat fails the whole scrape).
func mergeFamilies(dst, src map[string]*dto.MetricFamily) {
	for name, f := range src {
		have, ok := dst[name]
		if !ok {
			dst[name] = f
			continue
		}
		if have.GetType() != f.GetType() {
			continue
		}
		seen := map[string]bool{}
		for _, m := range have.GetMetric() {
			seen[labelKey(m)] = true
		}
		for _, m := range f.GetMetric() {
			if k := labelKey(m); !seen[k] {
				seen[k] = true
				have.Metric = append(have.Metric, m)
			}
		}
	}
}

func familyList(m map[string]*dto.MetricFamily) []*dto.MetricFamily {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*dto.MetricFamily, 0, len(names))
	for _, name := range names {
		out = append(out, m[name])
	}
	return out
}

// machinesMetrics is this machine's supervisor's families and the joined machines',
// asked at once (each bounded by its own 2 s) and merged into one set of families, the
// supervisor's first.
type machinesMetrics struct {
	local supervisorMetrics
	nodes *nodeMetrics
}

// Gather implements prometheus.Gatherer.
func (g machinesMetrics) Gather() ([]*dto.MetricFamily, error) {
	var nodes []*dto.MetricFamily
	done := make(chan struct{})
	go func() {
		defer close(done)
		nodes = g.nodes.Families()
	}()
	local, _ := g.local.Gather()
	<-done
	if len(nodes) == 0 {
		return local, nil
	}
	merged := map[string]*dto.MetricFamily{}
	for _, f := range local {
		merged[f.GetName()] = f
	}
	// the cached families are shared between scrapes: copied, so a merge never changes them
	// (the supervisor's are read afresh each scrape)
	src := make(map[string]*dto.MetricFamily, len(nodes))
	for _, f := range nodes {
		c := &dto.MetricFamily{Name: f.Name, Help: f.Help, Type: f.Type, Unit: f.Unit,
			Metric: append([]*dto.Metric(nil), f.GetMetric()...)}
		src[f.GetName()] = c
	}
	mergeFamilies(merged, src)
	return familyList(merged), nil
}

// newNodeMetrics asks the machines on the hub ref's channel (none while it is unset).
func newNodeMetrics(ref *router.HubRef) *nodeMetrics {
	return &nodeMetrics{hub: func() machineAsker {
		if h := ref.Get(); h != nil {
			return h
		}
		return nil
	}, wait: nodeMetricsWait, ttl: nodeMetricsTTL}
}
