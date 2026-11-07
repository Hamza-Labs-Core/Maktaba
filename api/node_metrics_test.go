package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
)

// metricsMachines is the cluster channel with each machine's agent answering "metrics" with
// what its supervisor's /metrics (an httptest server) serves.
type metricsMachines struct {
	urls  map[string]string // node id -> its supervisor
	asked atomic.Int32
}

func (f *metricsMachines) MachineAgents() map[string]string {
	out := map[string]string{}
	for node := range f.urls {
		out[node] = "session-" + node
	}
	return out
}

func (f *metricsMachines) CommandWithin(ctx context.Context, session, name string, _ json.RawMessage, _ string, wait time.Duration) (agent.CmdResult, error) {
	f.asked.Add(1)
	if name != "metrics" {
		return agent.CmdResult{}, agent.ErrBadCommand
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.urls[strings.TrimPrefix(session, "session-")]+"/metrics", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return agent.CmdResult{}, agent.ErrCmdTimeout
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return agent.CmdResult{OK: false, Detail: json.RawMessage(`"unknown command"`)}, nil
	}
	d, _ := json.Marshal(string(b))
	return agent.CmdResult{OK: true, Detail: d}, nil
}

func textServer(t *testing.T, body string, delay time.Duration) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if body == "" {
			http.Error(w, "no", http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

func scrapeMetrics(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// Story 53.12: the home's /metrics carries every joined machine's memory, disk, load and
// components beside the home's own, each series labelled with its machine; only series
// with a machine label are taken (a machine's update metrics or Go families would repeat
// the home's), a machine never speaks for another, and a machine that is slow, old or
// gone adds nothing and does not hold the scrape back.
func TestMetricsCarryTheJoinedMachines(t *testing.T) {
	home := textServer(t, "# HELP maktaba_host_load1 The one-minute load average.\n# TYPE maktaba_host_load1 gauge\n"+
		"maktaba_host_load1{machine=\"home\"} 1.5\n"+
		"# TYPE maktaba_component_up gauge\nmaktaba_component_up{id=\"api\",machine=\"home\",phase=\"running\"} 1\n", 0)
	studio := textServer(t, "# HELP maktaba_host_load1 The one-minute load average.\n# TYPE maktaba_host_load1 gauge\n"+
		"maktaba_host_load1{machine=\"studio\"} 3.25\n"+
		"# TYPE maktaba_host_disk_free_bytes gauge\nmaktaba_host_disk_free_bytes{machine=\"studio\"} 1e+12\n"+
		"# TYPE maktaba_component_up gauge\nmaktaba_component_up{id=\"api\",machine=\"studio\",phase=\"running\"} 1\n"+
		"# TYPE maktaba_component_restarts_total counter\nmaktaba_component_restarts_total{id=\"api\",machine=\"studio\"} 2\n"+
		// no machine label: the home has its own of these
		"# TYPE maktaba_update_runs_total counter\nmaktaba_update_runs_total{result=\"ok\"} 9\n"+
		"# TYPE maktaba_component_transition_seconds_count untyped\nmaktaba_component_transition_seconds_count{id=\"api\",to=\"running\"} 4\n"+
		"# TYPE go_goroutines gauge\ngo_goroutines{machine=\"studio\"} 99\n", 0)
	// a machine that says it is the home is put down as itself
	liar := textServer(t, "# TYPE maktaba_host_load1 gauge\nmaktaba_host_load1{machine=\"home\"} 42\n", 0)
	slow := textServer(t, "# TYPE maktaba_host_load1 gauge\nmaktaba_host_load1{machine=\"slow\"} 7\n", 10*time.Second)
	old := textServer(t, "", 0) // a build before the command: it answers no
	machines := &metricsMachines{urls: map[string]string{"studio": studio.URL, "liar": liar.URL, "slow": slow.URL, "old": old.URL}}
	nodes := &nodeMetrics{hub: func() machineAsker { return machines }, wait: 300 * time.Millisecond, ttl: time.Minute}

	start := time.Now()
	body := scrapeMetrics(t, metricsHandler(home.URL, nodes))
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the scrape waited %s for a slow machine", took)
	}
	for _, want := range []string{
		`maktaba_host_load1{machine="home"} 1.5`,
		`maktaba_host_load1{machine="studio"} 3.25`,
		`maktaba_host_load1{machine="liar"} 42`,
		`maktaba_host_disk_free_bytes{machine="studio"} 1e+12`,
		`maktaba_component_up{id="api",machine="home",phase="running"} 1`,
		`maktaba_component_up{id="api",machine="studio",phase="running"} 1`,
		`maktaba_component_restarts_total{id="api",machine="studio"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, not := range []string{`machine="slow"`, `maktaba_host_load1{machine="home"} 42`, `go_goroutines{machine=`,
		"maktaba_update_runs_total", "maktaba_component_transition_seconds"} {
		if strings.Contains(body, not) {
			t.Errorf("%s should not show", not)
		}
	}
	if n := strings.Count(body, "# TYPE maktaba_host_load1 "); n != 1 {
		t.Errorf("maktaba_host_load1 is %d families", n)
	}
	if t.Failed() {
		t.Log(body)
	}
}

// One round of asking serves the scrapes for the TTL (the machines are not asked on
// every scrape), and no channel (a node, a home before its channel) asks no one.
func TestNodeMetricsCached(t *testing.T) {
	studio := textServer(t, "# TYPE maktaba_host_load1 gauge\nmaktaba_host_load1{machine=\"studio\"} 1\n", 0)
	machines := &metricsMachines{urls: map[string]string{"studio": studio.URL}}
	now := time.Unix(1_800_000_000, 0)
	nodes := &nodeMetrics{hub: func() machineAsker { return machines }, wait: time.Second, ttl: 10 * time.Second,
		now: func() time.Time { return now }}
	for range 3 {
		if fams := nodes.Families(); len(fams) != 1 || fams[0].GetName() != "maktaba_host_load1" {
			t.Fatalf("families: %v", fams)
		}
	}
	if n := machines.asked.Load(); n != 1 {
		t.Fatalf("asked %d times within the TTL", n)
	}
	now = now.Add(11 * time.Second)
	nodes.Families()
	if n := machines.asked.Load(); n != 2 {
		t.Fatalf("asked %d times after the TTL", n)
	}
	// the cached families are not changed by the scrapes that merge them
	h := metricsHandler("", nodes)
	for range 2 {
		if body := scrapeMetrics(t, h); strings.Count(body, `maktaba_host_load1{machine="studio"} 1`) != 1 {
			t.Fatalf("scrape:\n%s", body)
		}
	}
	none := &nodeMetrics{hub: func() machineAsker { return nil }, ttl: time.Second}
	if fams := none.Families(); fams != nil {
		t.Fatalf("no channel: %v", fams)
	}
	if fams := (*nodeMetrics)(nil).Families(); fams != nil {
		t.Fatal("nil")
	}
	if fams := newNodeMetrics(nil).Families(); fams != nil {
		t.Fatal("an unset hub ref")
	}
}

// A machine's answer that is not the text format, or a refusal, adds nothing.
func TestNodeMetricsBadAnswers(t *testing.T) {
	for _, c := range []struct {
		name string
		res  agent.CmdResult
		err  error
	}{
		{"timeout", agent.CmdResult{}, agent.ErrCmdTimeout},
		{"refused", agent.CmdResult{OK: false, Detail: json.RawMessage(`"no"`)}, nil},
		{"not a string", agent.CmdResult{OK: true, Detail: json.RawMessage(`{"a":1}`)}, nil},
		{"not the text format", agent.CmdResult{OK: true, Detail: json.RawMessage(`"maktaba_host_load1{machine= oops"`)}, nil},
	} {
		got := askMachine(context.Background(), answering{c.res, c.err}, "studio", "s", time.Second)
		if len(got) != 0 {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

type answering struct {
	res agent.CmdResult
	err error
}

func (answering) MachineAgents() map[string]string { return map[string]string{"studio": "s"} }
func (a answering) CommandWithin(context.Context, string, string, json.RawMessage, string, time.Duration) (agent.CmdResult, error) {
	return a.res, a.err
}

// A joined machine's answer is not trusted: only its component and host families are
// taken (not its update metrics, nor any other maktaba_ family the home may have), and two
// series that become one after the machine label is rewritten are kept once, so a machine
// cannot make the home's /metrics repeat a series and fail the whole scrape.
func TestNodeMetricsUntrustedAnswer(t *testing.T) {
	text := `# TYPE maktaba_host_load1 gauge
maktaba_host_load1{machine="a"} 1
maktaba_host_load1{machine="b"} 2
# TYPE maktaba_update_runs_total counter
maktaba_update_runs_total{machine="a"} 3
# TYPE maktaba_pipeline_queue_depth gauge
maktaba_pipeline_queue_depth{machine="a",stage="x",state="pending"} 4
`
	d, _ := json.Marshal(text)
	got := askMachine(context.Background(), answering{agent.CmdResult{OK: true, Detail: d}, nil}, "studio", "s", time.Second)
	if len(got) != 1 || got["maktaba_host_load1"] == nil {
		t.Fatalf("families: %v", got)
	}
	ms := got["maktaba_host_load1"].GetMetric()
	if len(ms) != 1 || labelKey(ms[0]) != "machine\x00studio" || ms[0].GetGauge().GetValue() != 1 {
		t.Fatalf("series: %v", ms)
	}
}
