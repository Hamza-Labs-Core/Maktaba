// Package perf implements the API-side performance budget gate (Epic 18).
//
// The single source of truth is `shared/perf_budgets.yaml`. This package:
//
//   - Loads + validates that file (Story 18.1).
//   - Surfaces a Budget lookup so the perf-test harness can pull the
//     ceiling for a given endpoint+cache without magic numbers.
//   - Exposes a ResponseTimer middleware that emits `http_endpoint_p95_breach_total`
//     when a request's wall-clock duration is above its budget (Story 18.6).
//
// Plan-18-08 also owns a `/admin/cache/{name}/flush` endpoint for whole-
// cache invalidation; that lives in handlers/perf and consumes the Cache
// registry exposed here.
package perf

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Budgets is the parsed YAML.
type Budgets struct {
	Version          int                   `yaml:"version"`
	HardwareProfiles map[string]string     `yaml:"hardware_profiles"`
	Endpoints        map[string]Budget     `yaml:"endpoints"`
	Throughputs      map[string]Throughput `yaml:"throughputs"`
	Envelopes        map[string]Envelope   `yaml:"envelopes"`
}

// Budget is one endpoint entry.
type Budget struct {
	Surface string `yaml:"surface"`
	Method  string `yaml:"method,omitempty"`
	Path    string `yaml:"path"`
	Profile string `yaml:"profile"`
	Cache   string `yaml:"cache"`
	P50ms   int    `yaml:"p50_ms,omitempty"`
	P95ms   int    `yaml:"p95_ms"`
	P99ms   int    `yaml:"p99_ms,omitempty"`
	CIPR    bool   `yaml:"ci_pr"`
}

// Throughput is a target rate.
type Throughput struct {
	Profile string `yaml:"profile"`
	Target  int    `yaml:"target"`
}

// Envelope is a resource ceiling.
type Envelope struct {
	Profile string `yaml:"profile"`
	P95MB   int    `yaml:"p95_mb,omitempty"`
	P95Pct  int    `yaml:"p95_pct,omitempty"`
}

// Load parses the YAML at path and validates it.
func Load(path string) (*Budgets, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var bg Budgets
	if err := yaml.Unmarshal(b, &bg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := bg.Validate(); err != nil {
		return nil, err
	}
	return &bg, nil
}

// Validate enforces structural invariants. Per Story 18.1 TC-1:
//   - missing key, negative ms, p99 < p95, unknown profile all fail.
func (b *Budgets) Validate() error {
	if b.Version <= 0 {
		return errors.New("budgets: version required")
	}
	if len(b.HardwareProfiles) == 0 {
		return errors.New("budgets: hardware_profiles required")
	}
	for id, ep := range b.Endpoints {
		if ep.Path == "" {
			return fmt.Errorf("endpoint %s: path required", id)
		}
		if ep.Profile == "" {
			return fmt.Errorf("endpoint %s: profile required", id)
		}
		if _, ok := b.HardwareProfiles[ep.Profile]; !ok {
			return fmt.Errorf("endpoint %s: unknown profile %q", id, ep.Profile)
		}
		if ep.Cache != "warm" && ep.Cache != "cold" && ep.Cache != "" {
			return fmt.Errorf("endpoint %s: cache must be warm|cold (got %q)", id, ep.Cache)
		}
		if ep.P50ms < 0 || ep.P95ms < 0 || ep.P99ms < 0 {
			return fmt.Errorf("endpoint %s: negative ms", id)
		}
		if ep.P95ms == 0 {
			return fmt.Errorf("endpoint %s: p95_ms required", id)
		}
		if ep.P99ms > 0 && ep.P99ms < ep.P95ms {
			return fmt.Errorf("endpoint %s: p99 (%d) < p95 (%d)", id, ep.P99ms, ep.P95ms)
		}
		if ep.P50ms > 0 && ep.P50ms > ep.P95ms {
			return fmt.Errorf("endpoint %s: p50 (%d) > p95 (%d)", id, ep.P50ms, ep.P95ms)
		}
	}
	for id, tp := range b.Throughputs {
		if tp.Target <= 0 {
			return fmt.Errorf("throughput %s: target must be > 0", id)
		}
	}
	return nil
}

// CISubset returns the subset that the on-PR run enforces.
func (b *Budgets) CISubset() []Budget {
	out := []Budget{}
	for _, ep := range b.Endpoints {
		if ep.CIPR {
			out = append(out, ep)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Lookup finds the budget for the given endpoint+cache combination, or
// nil if none is registered.
func (b *Budgets) Lookup(method, path, cache string) *Budget {
	for _, ep := range b.Endpoints {
		if ep.Method == method && ep.Path == path && (ep.Cache == cache || cache == "") {
			cp := ep
			return &cp
		}
	}
	return nil
}

// Breach is a single budget violation reported by the harness.
type Breach struct {
	Endpoint string
	Path     string
	BudgetMs int
	GotMs    int
}

func (b Breach) String() string {
	return fmt.Sprintf("%s (%s): budget p95=%dms, observed=%dms (Δ=%dms)",
		b.Endpoint, b.Path, b.BudgetMs, b.GotMs, b.GotMs-b.BudgetMs)
}

// Gauge tracks the observed p95s for a run and reports breaches.
type Gauge struct {
	mu     sync.Mutex
	values map[string][]time.Duration
}

// NewGauge creates a Gauge.
func NewGauge() *Gauge { return &Gauge{values: map[string][]time.Duration{}} }

// Observe records a duration for an endpoint id.
func (g *Gauge) Observe(endpointID string, d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.values[endpointID] = append(g.values[endpointID], d)
}

// Report compares the observed p95s against b's budgets and returns the
// list of breaches.
func (g *Gauge) Report(b *Budgets) []Breach {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := []Breach{}
	for id, ds := range g.values {
		ep, ok := b.Endpoints[id]
		if !ok {
			continue
		}
		got := p95(ds)
		if int(got/time.Millisecond) > ep.P95ms {
			out = append(out, Breach{
				Endpoint: id,
				Path:     ep.Path,
				BudgetMs: ep.P95ms,
				GotMs:    int(got / time.Millisecond),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Endpoint < out[j].Endpoint })
	return out
}

// p95 returns the 95th percentile by index (nearest-rank).
func p95(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(ds))
	copy(sorted, ds)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := (len(sorted) * 95) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
