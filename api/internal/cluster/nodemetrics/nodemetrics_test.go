package nodemetrics

import (
	"sort"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/schemaguard"
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
	paused = func() string { return schemaguard.PauseHomeBehind }
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

// The paused label is the schema guard's code, never words of its message; a code this
// build does not know is other.
func TestPausedLabelIsTheGuardsCode(t *testing.T) {
	for code, want := range map[string]string{schemaguard.PauseHomeBehind: "home_behind",
		schemaguard.PauseNodeBehind: "node_behind", "future_code": "other"} {
		reg := prometheus.NewRegistry()
		reg.MustRegister(pausedCollector{desc: prometheus.NewDesc(MetricPaused, "x", []string{"reason"}, nil)})
		pausedMu.Lock()
		paused = func() string { return code }
		pausedMu.Unlock()
		f := gather(t, reg)[MetricPaused]
		if got := labels(f); len(got) != 1 || got[0] != "reason="+want {
			t.Errorf("%s: %v", code, got)
		}
	}
	pausedMu.Lock()
	paused = nil
	pausedMu.Unlock()
	h := schemaguard.HomeSchema{Known: true, Applied: 150}
	if schemaguard.PauseCode(h, 152) != schemaguard.PauseHomeBehind ||
		schemaguard.PauseCode(schemaguard.HomeSchema{Known: true, Applied: 152, HasMinNode: true, MinNode: 153}, 152) != schemaguard.PauseNodeBehind ||
		schemaguard.PauseCode(schemaguard.HomeSchema{Known: true, Applied: 152}, 152) != "" {
		t.Fatal("the guard's codes")
	}
}
