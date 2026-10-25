package nodemetrics

import (
	"sort"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func gather(t *testing.T, reg *prometheus.Registry) map[string]*dto.MetricFamily {
	t.Helper()
	fams, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*dto.MetricFamily{}
	for _, f := range fams {
		out[f.GetName()] = f
	}
	return out
}

func labels(f *dto.MetricFamily) []string {
	var out []string
	for _, m := range f.GetMetric() {
		for _, l := range m.GetLabel() {
			out = append(out, l.GetName()+"="+l.GetValue())
		}
	}
	sort.Strings(out)
	return out
}

// TC1 (full nodes): the node's metrics with their labels; a nil Metrics (a home, a
// worker) counts nothing and registers nothing (EC2: absent series, not zeros).
func TestNodeMetricsRegistered(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg)
	pausedMu.Lock()
	paused = func() string { return "paused: home database at 0150, this machine needs 0152 — update the home" }
	pausedMu.Unlock()
	t.Cleanup(func() {
		pausedMu.Lock()
		paused = nil
		pausedMu.Unlock()
	})
	m.Delegate(DelegateOK)
	m.Delegate(DelegateUnreachable)
	m.CacheHit()
	m.Served("home", 200)
	m.Served("any", 503)
	fams := gather(t, reg)
	for name, want := range map[string]string{
		MetricDelegateRequests:  "result=ok",
		MetricDelegateCacheHits: "",
		MetricProxyRequests:     "class=any",
		MetricPaused:            "reason=home_behind",
	} {
		f, ok := fams[name]
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if want == "" {
			continue
		}
		found := false
		for _, l := range labels(f) {
			if l == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: labels %v, want %s", name, labels(f), want)
		}
	}
	if v := fams[MetricDelegateCacheHits].GetMetric()[0].GetCounter().GetValue(); v != 1 {
		t.Fatalf("cache hits %v", v)
	}
	if v := fams[MetricPaused].GetMetric()[0].GetGauge().GetValue(); v != 1 {
		t.Fatalf("paused %v", v)
	}

	var none *Metrics
	none.Delegate(DelegateOK)
	none.CacheHit()
	none.Served("home", 200)
	if fams := gather(t, prometheus.NewRegistry()); len(fams) != 0 {
		t.Fatalf("a machine that is no full node has series: %v", fams)
	}
}

func TestPauseCode(t *testing.T) {
	for reason, want := range map[string]string{
		"paused: home database at 0150, this machine needs 0152 — update the home":                              "home_behind",
		"paused: home database at 0152 needs this machine at 0152 or newer (it has 0150) — update this machine": "node_behind",
		"something else": "other",
	} {
		if got := PauseCode(reason); got != want {
			t.Errorf("%q: %s, want %s", reason, got, want)
		}
	}
}
