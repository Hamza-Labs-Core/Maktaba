//go:build broadcastrehearsal

package rehearsal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// broadcast-observe's monitoring (50.28; compose.observe.yml): the witness's Prometheus with the
// production rules, Alertmanager with the production routes, the Pushgateway, and the cloud's
// series (the monitor). Its ports on the runner:
const (
	prometheusPort   = 19090
	alertmanagerPort = 19093
	pushgatewayPort  = 19091
	monitorPort      = 19095
	// observeBudgetUSD is broadcast.monthly_budget_usd in the rehearsal: 80 % of it is the cost
	// model's fixed $5 (Workers Paid), so the month's estimate passes it with the first live
	// channel-minute the monitor folds in
	observeBudgetUSD = 6.25
	// observeNtfyToken is the stand-in ntfy topic's bearer: a label, never a real token
	observeNtfyToken = "rehearsal-ntfy-token-not-real"
	// lateClock is the canary's burned clock read late (BROADCAST_OCR_CMD): 45 s behind
	lateClock = `late_clock() { echo $(( $(date +%s) - 45 )); }; late_clock`
)

// alertmanagerVars render deploy/cloud-ha/monitoring/alertmanager.yml as setup.sh does, with
// stand-ins: the phone receiver pages the monitor's ntfy stand-in; the email digest's server is
// nothing (SEV3 waits an hour before its first digest, longer than the scenario).
var alertmanagerVars = map[string]string{
	"NTFY_URL":         "http://monitor:9090/ntfy",
	"NTFY_TOKEN":       observeNtfyToken,
	"ALERT_EMAIL":      "ops@rehearsal.invalid",
	"ALERT_EMAIL_FROM": "alerts@rehearsal.invalid",
	"SMTP_HOST":        "monitor:2525",
	"SMTP_USER":        "rehearsal",
	"SMTP_PASSWORD":    "rehearsal-not-a-password",
}

// envVar is a ${NAME} setup.sh's envsubst fills (a comment's "${...}" is not one).
var envVar = regexp.MustCompile(`\$\{[A-Z_][A-Z0-9_]*\}`)

// observeEnv renders alertmanager.yml for compose.observe.yml and names the monitoring's ports.
func observeEnv(t *testing.T) []string {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy/cloud-ha/monitoring/alertmanager.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	out := envVar.ReplaceAllStringFunc(string(raw), func(m string) string {
		k := m[2 : len(m)-1]
		v, ok := alertmanagerVars[k]
		if !ok {
			missing = append(missing, k)
		}
		return v
	})
	if len(missing) > 0 {
		t.Fatalf("alertmanager.yml has variables the rehearsal does not render: %v", missing)
	}
	dir := t.TempDir()
	// the images run as nobody: the directory and file must be readable to it
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alertmanager.yml"), []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	return []string{"OBSERVE_DIR=" + dir, "OBSERVE_NTFY_TOKEN=" + observeNtfyToken,
		fmt.Sprintf("OBSERVE_BUDGET_USD=%g", observeBudgetUSD),
		fmt.Sprintf("PROMETHEUS_PORT=%d", prometheusPort), fmt.Sprintf("ALERTMANAGER_PORT=%d", alertmanagerPort),
		fmt.Sprintf("PUSHGATEWAY_PORT=%d", pushgatewayPort), fmt.Sprintf("MONITOR_PORT=%d", monitorPort)}
}

// round is one minute of the canary probes: network 1 (the witness's broadcast-probe.sh) and
// network 2 (probe.sh on GitHub's runners, which plays the canary too), and the status page's
// Broadcasting component after the round was folded in.
type round struct {
	At       time.Time
	Witness  bool
	Actions  bool
	Status   string
	Glass    string // the witness's glass-to-glass, as the probe printed it
	Problems string
}

// observer runs the canary probes every 60 s, as the witness and cloud-probe.yml do, and reads
// the monitoring.
type observer struct {
	s       *stack
	root    string
	work    string
	prom    string
	am      string
	monitor string
	pg      string
	pass    *httptest.Server
	late    atomic.Bool

	mu     sync.Mutex
	rounds []round
}

func (s *stack) observer(ch string) *observer {
	o := &observer{s: s, root: repoRoot(s.t), work: s.t.TempDir(),
		prom: fmt.Sprintf("http://127.0.0.1:%d", prometheusPort), am: fmt.Sprintf("http://127.0.0.1:%d", alertmanagerPort),
		monitor: fmt.Sprintf("http://127.0.0.1:%d", monitorPort), pg: fmt.Sprintf("http://127.0.0.1:%d", pushgatewayPort)}
	// the canary pass (50.28 §5 step 1): redeemed, it answers the master playlist's URL
	var n atomic.Int64
	o.pass = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		tok, err := s.viewerToken(ch)(fmt.Sprintf("canary-probe-%d", n.Add(1)))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONTo(w, map[string]string{"master": s.edge + "/" + tok + "/" + ch + "/master.m3u8"})
	}))
	s.t.Cleanup(o.pass.Close)
	s.t.Cleanup(o.keep)
	return o
}

func writeJSONTo(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// keep copies the probes' rounds and the status data into the scenario's output (the logs rule).
func (o *observer) keep() {
	dst := filepath.Join(outDir(o.s.t), "broadcast-observe-status")
	_ = os.MkdirAll(dst, 0o755)
	for _, f := range []string{"rounds", "network1.json", "status/checks/state.json"} {
		if b, err := os.ReadFile(filepath.Join(o.work, f)); err == nil {
			_ = os.WriteFile(filepath.Join(dst, strings.ReplaceAll(f, "/", "_")), b, 0o644)
		}
	}
}

func (o *observer) script(name string, env ...string) *exec.Cmd {
	cmd := exec.Command("bash", filepath.Join(o.root, name))
	cmd.Env = append(os.Environ(), env...)
	return cmd
}

// probeRound is one minute: the witness plays the canary and pushes, status-export writes its
// view, network 2 plays it and records the status page's round, and the round is folded in.
func (o *observer) probeRound() round {
	r := round{At: time.Now()}
	common := []string{"BROADCAST_PASS_URL=" + o.pass.URL + "/pass", "PUSHGATEWAY_URL=" + o.pg}
	if o.late.Load() {
		common = append(common, "BROADCAST_OCR_CMD="+lateClock)
	}

	w := o.script("deploy/cloud-ha/monitoring/broadcast-probe.sh",
		append(common, "NETWORK=witness", "STATE_FILE="+filepath.Join(o.work, "witness.state"))...)
	out, err := w.Output()
	r.Witness = err == nil
	var res struct {
		Reason string   `json:"reason"`
		Glass  *float64 `json:"glass_to_glass_s"`
	}
	if json.Unmarshal(out, &res) == nil {
		if res.Glass != nil {
			r.Glass = strconv.FormatFloat(*res.Glass, 'f', 1, 64)
		}
		if res.Reason != "" {
			r.Problems = "witness: " + res.Reason
		}
	}

	exp := o.script("deploy/cloud-ha/bin/status-export", "PROMETHEUS="+o.prom, "OUT="+filepath.Join(o.work, "network1.json"))
	if b, err := exp.CombinedOutput(); err != nil {
		r.Problems += fmt.Sprintf(" status-export: %v %s", err, b)
	}

	rounds := filepath.Join(o.work, "rounds")
	p := o.script("deploy/cloud-ha/monitoring/probe.sh", append(common, "ROUNDS=1", "EVERY=0", "ROUNDS_FILE="+rounds,
		"CANARY_URL="+o.pass.URL+"/health", "API_URL="+o.pass.URL+"/health",
		"NETWORK1_URL=file://"+filepath.Join(o.work, "network1.json"))...)
	pout, _ := p.CombinedOutput() // one round exits 0 whatever it found; the round is in the file
	r.Actions = strings.Contains(string(pout), "broadcast 1")

	fold := exec.Command("node", filepath.Join(o.root, "status/update.mjs"), "--data", filepath.Join(o.work, "status"), "--rounds", rounds)
	if b, err := fold.CombinedOutput(); err != nil {
		r.Problems += fmt.Sprintf(" status fold: %v %s", err, b)
	}
	r.Status = o.statusState()
	o.s.t.Logf("canary round: witness %v (g2g %s s), actions %v, status page Broadcasting %q %s", r.Witness, r.Glass, r.Actions, r.Status, strings.TrimSpace(r.Problems))
	o.mu.Lock()
	o.rounds = append(o.rounds, r)
	o.mu.Unlock()
	return r
}

// statusState is the status page's Broadcasting component (checks/state.json).
func (o *observer) statusState() string {
	var st struct {
		Components map[string]struct {
			State string `json:"state"`
		} `json:"components"`
	}
	b, err := os.ReadFile(filepath.Join(o.work, "status/checks/state.json"))
	if err != nil || json.Unmarshal(b, &st) != nil {
		return ""
	}
	return st.Components["broadcasting"].State
}

// run probes every 60 s until ctx ends (the probes' production cadence).
func (o *observer) run(ctx context.Context) {
	go func() {
		for ctx.Err() == nil {
			o.probeRound()
			viewersSleep(ctx, 60*time.Second)
		}
	}()
}

func (o *observer) history() []round {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]round(nil), o.rounds...)
}

// took records that the edge took a revocation list now (broadcast.RecordRevocations).
func (o *observer) took() {
	code, raw, err := o.s.do(http.MethodPost, o.monitor+"/revocations/took", nil, nil)
	o.s.must("the cloud records the edge's take of the list", err == nil && code == http.StatusOK, code, string(raw), err)
}

// alert is one of Prometheus's alerts (/api/v1/alerts).
type alert struct {
	Name  string
	State string // pending or firing
	Value string
}

func (o *observer) alerts() map[string]alert {
	var a struct {
		Data struct {
			Alerts []struct {
				Labels map[string]string `json:"labels"`
				State  string            `json:"state"`
				Value  string            `json:"value"`
			} `json:"alerts"`
		} `json:"data"`
	}
	code, raw, err := o.s.do(http.MethodGet, o.prom+"/api/v1/alerts", nil, nil)
	if err != nil || code != http.StatusOK || json.Unmarshal(raw, &a) != nil {
		o.s.t.Logf("prometheus alerts: %d %v", code, err)
		return nil
	}
	out := map[string]alert{}
	for _, x := range a.Data.Alerts {
		n := x.Labels["alertname"]
		if cur, ok := out[n]; !ok || cur.State != "firing" {
			out[n] = alert{Name: n, State: x.State, Value: x.Value}
		}
	}
	return out
}

func (o *observer) firing(name string) bool { return o.alerts()[name].State == "firing" }

// route is the Alertmanager receiver an active alert is grouped under ("" when it has none).
func (o *observer) route(name string) string {
	var groups []struct {
		Receiver struct {
			Name string `json:"name"`
		} `json:"receiver"`
		Alerts []struct {
			Labels map[string]string `json:"labels"`
		} `json:"alerts"`
	}
	code, raw, err := o.s.do(http.MethodGet, o.am+"/api/v2/alerts/groups", nil, nil)
	if err != nil || code != http.StatusOK || json.Unmarshal(raw, &groups) != nil {
		o.s.t.Logf("alertmanager groups: %d %v", code, err)
		return ""
	}
	for _, g := range groups {
		for _, a := range g.Alerts {
			if a.Labels["alertname"] == name {
				return g.Receiver.Name
			}
		}
	}
	return ""
}

// fires waits for an alert to fire and for Alertmanager to route it, and checks the receiver
// (alertmanager.yml: SEV1 and SEV2 page the phone, SEV3 is the email digest).
func (o *observer) fires(name, receiver string, within time.Duration, why string) time.Duration {
	d, ok := waitFor(within, func() bool { return o.firing(name) && o.route(name) != "" })
	a := o.alerts()[name]
	got := o.route(name)
	o.s.check(fmt.Sprintf("%s fires (%s)", name, why), ok, "alert_"+name, fmt.Sprintf("after %s; state %q value %s", d.Round(time.Second), a.State, a.Value))
	o.s.check(fmt.Sprintf("%s is routed to %s", name, receiver), got == receiver, "route_"+name, "receiver:", got)
	o.s.timing(name+"_after", d)
	return d
}

// query is a PromQL query's samples, by their instance label (or "" for an aggregate).
func (o *observer) query(q string) map[string]float64 {
	var a struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	code, raw, err := o.s.do(http.MethodGet, o.prom+"/api/v1/query?query="+url.QueryEscape(q), nil, nil)
	if err != nil || code != http.StatusOK || json.Unmarshal(raw, &a) != nil {
		return nil
	}
	out := map[string]float64{}
	for _, r := range a.Data.Result {
		if len(r.Value) == 2 {
			if s, ok := r.Value[1].(string); ok {
				if v, err := strconv.ParseFloat(s, 64); err == nil {
					out[r.Metric["instance"]] = v
				}
			}
		}
	}
	return out
}

// page is a webhook Alertmanager sent the ntfy stand-in.
type page struct {
	At     time.Time `json:"at"`
	AuthOK bool      `json:"auth_ok"`
	Body   struct {
		Status string `json:"status"`
		Alerts []struct {
			Status string            `json:"status"`
			Labels map[string]string `json:"labels"`
		} `json:"alerts"`
	} `json:"body"`
}

// paged is the first page of a firing alert by name, if the phone got one.
func (o *observer) paged(name string) (page, bool) {
	var pages []page
	code, raw, err := o.s.do(http.MethodGet, o.monitor+"/ntfy", nil, nil)
	if err != nil || code != http.StatusOK || json.Unmarshal(raw, &pages) != nil {
		return page{}, false
	}
	for _, p := range pages {
		for _, a := range p.Body.Alerts {
			if a.Labels["alertname"] == name && a.Status == "firing" {
				return p, true
			}
		}
	}
	return page{}, false
}

// uplink sets the home's upload link (every Nth segment PUT's body over bodyMS).
func (s *stack) uplink(every int, bodyMS int) {
	body, _ := json.Marshal(map[string]int{"every": every, "body_ms": bodyMS})
	code, raw, err := s.do(http.MethodPost, s.home+"/uplink", body, nil)
	s.must("the home's uplink is set", err == nil && code == http.StatusOK, code, string(raw), err)
}
