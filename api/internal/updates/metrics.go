package updates

// Story 35.15 AC1: the update flow's Prometheus metrics, on the API's /metrics (admin
// port). Every number is read from the database when Prometheus scrapes (at most every
// metricsCacheFor), not counted in memory: an API that restarts mid-run (an update
// restarts it) shows the same counts after as before (EC1). The counters and histograms
// never go down: the runs and targets they count are rows that are never deleted (each
// histogram is summed by the database over all of them); the machines' steps (whose rows
// a machine's journal rewrites) and verification failures are running totals kept with
// the settings, counted once each (events.go countStep, Store.SetVerifyError). The supervisor's own
// (the database step's migration time) are its /metrics, merged into the API's
// (cmd/maktaba-server/internal/updater/metrics.go).
//
// Labels are bounded: run and target states, the known step names (anything else a
// machine reports is "other"), verification failure kinds. Never a run id, a node id or a
// version.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	metrics "github.com/Hamza-Labs-Core/Maktaba/shared/metrics/go"
)

// Metric names (design §16).
const (
	MetricRuns           = "maktaba_update_runs_total"
	MetricRunDuration    = "maktaba_update_run_duration_seconds"
	MetricStepDuration   = "maktaba_update_step_duration_seconds"
	MetricBackupBytes    = "maktaba_update_backup_bytes"
	MetricTargets        = "maktaba_update_targets"
	MetricNodeApply      = "maktaba_update_node_apply_duration_seconds"
	MetricVerifyFailures = "maktaba_update_verify_failures_total"
	MetricMachinesBehind = "maktaba_update_machines_behind"
	MetricLastCheck      = "maktaba_update_last_check_timestamp"
	metricsCacheFor      = 15 * time.Second
	metricsQueryTimeout  = 5 * time.Second
	// stepOther is the label of a step this build does not know
	stepOther = "other"
)

// Histogram buckets (seconds): a run is minutes to hours; a step seconds to an hour; a
// machine's apply (its switch until it is healthy) about a minute to an hour.
var (
	runBuckets   = []float64{60, 300, 600, 1800, 3600, 7200, 14400, 28800}
	stepBuckets  = []float64{1, 5, 15, 60, 300, 900, 1800, 3600}
	applyBuckets = []float64{30, 60, 120, 300, 600, 1200, 1800, 3600}
)

// targetStates are every target state, so a state that empties reads 0 (a test holds it
// to the Target* constants).
var targetStates = []string{TargetPending, TargetOffline, TargetPreparing, TargetPrepared, TargetFailedPrepare,
	TargetApplying, TargetHealthy, TargetRolledBack, TargetFailed, TargetHeld, TargetSkipped, TargetWorkerOnly,
	TargetOrdered, TargetAppStaged, TargetWaitingClick, TargetClicked, TargetNeedsPerson, TargetConverting}

// knownSteps are the steps a machine's journal records (cmd/maktaba-server/internal/updater
// Step*; a test holds them together): the step label's values, any other is stepOther.
var knownSteps = map[string]bool{"preflight": true, "stage": true, "smoke": true, "rehearse": true, "order": true,
	"backup": true, "switch": true, "restart": true, "watch": true, "commit": true, "launcher": true, "rollback": true, "tv": true}

// stepLabel is a step's label value.
func stepLabel(step string) string {
	if knownSteps[step] {
		return step
	}
	return stepOther
}

// Metrics is the update flow's prometheus.Collector.
type Metrics struct {
	Driver *Driver
	// Current is the release this home runs (the cluster's release when it is newer
	// than the watermark: a home installed by hand).
	Current func() string

	descRuns, descRunDur, descStepDur, descBackup, descTargets, descApply,
	descVerify, descBehind, descLastCheck *prometheus.Desc

	mu   sync.Mutex
	snap *metricsSnap
}

// NewMetrics returns the collector over d (register it once per process).
func NewMetrics(d *Driver, current func() string) *Metrics {
	return &Metrics{Driver: d, Current: current,
		descRuns:      prometheus.NewDesc(MetricRuns, "Update runs that ended, by their outcome (the run's last state).", []string{"result"}, nil),
		descRunDur:    prometheus.NewDesc(MetricRunDuration, "How long the update runs that ended took.", nil, nil),
		descStepDur:   prometheus.NewDesc(MetricStepDuration, "How long each machine's update steps took (done or failed).", []string{"step"}, nil),
		descBackup:    prometheus.NewDesc(MetricBackupBytes, "Size of the database dump the newest update took before its migrations.", nil, nil),
		descTargets:   prometheus.NewDesc(MetricTargets, "The machines of the active update run (else the last one), by state.", []string{"state"}, nil),
		descApply:     prometheus.NewDesc(MetricNodeApply, "How long a machine's apply took, from its switch until it was healthy.", nil, nil),
		descVerify:    prometheus.NewDesc(MetricVerifyFailures, "Releases whose verification failed, by the kind of failure.", []string{"reason"}, nil),
		descBehind:    prometheus.NewDesc(MetricMachinesBehind, "Joined machines on an older release than the cluster's.", nil, nil),
		descLastCheck: prometheus.NewDesc(MetricLastCheck, "When the home last looked for releases (Unix seconds).", nil, nil),
	}
}

var (
	exposeOnce sync.Once
	exposed    atomic.Pointer[Metrics]
)

// Expose puts m on the process's /metrics (the shared registry). The latest one mounted
// answers: a process mounts one, tests several.
func Expose(m *Metrics) {
	exposed.Store(m)
	exposeOnce.Do(func() { metrics.MustRegister(exposedCollector{}) })
}

// exposedCollector is the registry's view of the exposed Metrics (an unchecked collector:
// it describes nothing up front).
type exposedCollector struct{}

func (exposedCollector) Describe(chan<- *prometheus.Desc) {}

func (exposedCollector) Collect(ch chan<- prometheus.Metric) {
	if m := exposed.Load(); m != nil {
		m.Collect(ch)
	}
}

// Describe implements prometheus.Collector.
func (m *Metrics) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{m.descRuns, m.descRunDur, m.descStepDur, m.descBackup, m.descTargets,
		m.descApply, m.descVerify, m.descBehind, m.descLastCheck} {
		ch <- d
	}
}

// metricsSnap is one read of the numbers.
type metricsSnap struct {
	at        time.Time
	runs      map[string]float64
	runDur    histo
	stepDur   map[string]*histo
	apply     histo
	targets   map[string]float64
	backup    *float64
	verify    map[string]float64
	behind    *float64
	lastCheck *float64
}

// Collect implements prometheus.Collector.
func (m *Metrics) Collect(ch chan<- prometheus.Metric) {
	s := m.snapshot()
	for state, n := range s.runs {
		ch <- prometheus.MustNewConstMetric(m.descRuns, prometheus.CounterValue, n, state)
	}
	ch <- s.runDur.metric(m.descRunDur, runBuckets)
	for step, h := range s.stepDur {
		ch <- h.metric(m.descStepDur, stepBuckets, step)
	}
	if s.backup != nil {
		ch <- prometheus.MustNewConstMetric(m.descBackup, prometheus.GaugeValue, *s.backup)
	}
	for _, state := range targetStates {
		ch <- prometheus.MustNewConstMetric(m.descTargets, prometheus.GaugeValue, s.targets[state], state)
	}
	ch <- s.apply.metric(m.descApply, applyBuckets)
	for reason, n := range s.verify {
		ch <- prometheus.MustNewConstMetric(m.descVerify, prometheus.CounterValue, n, reason)
	}
	if s.behind != nil {
		ch <- prometheus.MustNewConstMetric(m.descBehind, prometheus.GaugeValue, *s.behind)
	}
	if s.lastCheck != nil {
		ch <- prometheus.MustNewConstMetric(m.descLastCheck, prometheus.GaugeValue, *s.lastCheck)
	}
}

// histo is a histogram the database summed: how many, their sum, and how many at or
// under each bucket (cumulative).
type histo struct {
	count  uint64
	sum    float64
	counts []uint64
}

func (h *histo) add(o histo) {
	h.count += o.count
	h.sum += o.sum
	if h.counts == nil {
		h.counts = make([]uint64, len(o.counts))
	}
	for i, c := range o.counts {
		h.counts[i] += c
	}
}

func (h histo) metric(desc *prometheus.Desc, buckets []float64, labels ...string) prometheus.Metric {
	counts := make(map[float64]uint64, len(buckets))
	for i, b := range buckets {
		if i < len(h.counts) {
			counts[b] = h.counts[i]
		}
	}
	return prometheus.MustNewConstHistogram(desc, h.count, h.sum, counts, labels...)
}

// seconds is the SQL of a row's duration in seconds, from two timestamp columns.
func (m *Metrics) seconds(from, to string) string {
	if m.Driver.Store.sqlite() {
		return "(julianday(" + to + ") - julianday(" + from + ")) * 86400.0"
	}
	return "EXTRACT(EPOCH FROM (" + to + " - " + from + "))"
}

// histograms sums the durations of rows by group: inner is a query of (g, d) rows, d in
// seconds ("" groups: one).
func (m *Metrics) histograms(ctx context.Context, inner string, buckets []float64) (map[string]*histo, error) {
	q := "SELECT g, count(*), COALESCE(sum(d), 0)"
	for _, b := range buckets {
		q += fmt.Sprintf(", COALESCE(sum(CASE WHEN d <= %g THEN 1 ELSE 0 END), 0)", b)
	}
	rows, err := m.Driver.Store.DB.QueryContext(ctx, q+" FROM ("+inner+") x WHERE d IS NOT NULL GROUP BY g")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]*histo{}
	for rows.Next() {
		var g string
		h := histo{counts: make([]uint64, len(buckets))}
		dest := []any{&g, &h.count, &h.sum}
		for i := range h.counts {
			dest = append(dest, &h.counts[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		if cur, ok := out[g]; ok {
			cur.add(h)
		} else {
			out[g] = &h
		}
	}
	return out, rows.Err()
}

func (m *Metrics) snapshot() *metricsSnap {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.Driver.now()
	if m.snap != nil && now.Sub(m.snap.at) < metricsCacheFor {
		return m.snap
	}
	ctx, cancel := context.WithTimeout(context.Background(), metricsQueryTimeout)
	defer cancel()
	m.snap = m.read(ctx, now)
	return m.snap
}

// read takes every number from the database (a query that fails leaves its numbers out).
func (m *Metrics) read(ctx context.Context, now time.Time) *metricsSnap {
	s := &metricsSnap{at: now, runs: map[string]float64{}, stepDur: map[string]*histo{},
		targets: map[string]float64{}, verify: map[string]float64{}}
	st := m.Driver.Store
	db := st.DB
	if rows, err := db.QueryContext(ctx, `SELECT state, count(*) FROM update_runs WHERE finished_at IS NOT NULL
		GROUP BY state`); err == nil {
		for rows.Next() {
			var state string
			var n float64
			if rows.Scan(&state, &n) == nil {
				s.runs[state] = n
			}
		}
		_ = rows.Close()
	}
	if hs, err := m.histograms(ctx, `SELECT '' AS g, `+m.seconds("started_at", "finished_at")+` AS d
		FROM update_runs WHERE finished_at IS NOT NULL`, runBuckets); err == nil && hs[""] != nil {
		s.runDur = *hs[""]
	}
	if hs, err := m.histograms(ctx, `SELECT '' AS g, `+m.seconds("applied_at", "healthy_at")+` AS d
		FROM update_targets WHERE applied_at IS NOT NULL AND healthy_at IS NOT NULL`, applyBuckets); err == nil && hs[""] != nil {
		s.apply = *hs[""]
	}
	var raw nullJSON
	if err := db.QueryRowContext(ctx, `SELECT backup FROM update_runs WHERE backup IS NOT NULL
		ORDER BY started_at DESC LIMIT 1`).Scan(&raw); err == nil && len(raw.b) > 0 {
		var b RunBackup
		if json.Unmarshal(raw.b, &b) == nil && b.Bytes > 0 {
			v := float64(b.Bytes)
			s.backup = &v
		}
	}
	run, err := st.ActiveRun(ctx)
	if err == nil && run == nil {
		if last, lerr := st.Runs(ctx, 1); lerr == nil && len(last) > 0 {
			run = &last[0]
		}
	}
	if run != nil {
		if ts, terr := st.Targets(ctx, run.ID); terr == nil {
			for _, t := range ts {
				s.targets[t.State]++
			}
		}
	}
	counters := st.Counters(ctx)
	for kind, n := range counters.VerifyFailures {
		s.verify[kind] = float64(n)
	}
	for label, h := range counters.Steps {
		if h == nil || len(h.Counts) != len(stepBuckets) {
			continue // counted over other bounds: not shown under these
		}
		s.stepDur[stepLabel(label)] = &histo{count: h.Count, sum: h.Sum, counts: h.Counts}
	}
	if n, ok := m.machinesBehind(ctx); ok {
		v := float64(n)
		s.behind = &v
	}
	if ls := st.LoadState(ctx); ls.LastCheckAt != nil {
		v := float64(ls.LastCheckAt.Unix())
		s.lastCheck = &v
	}
	return s
}

// machinesBehind counts the joined machines on an older release than the cluster's (the
// watermark, or the home's own release when it is newer): a full node, or any machine
// whose agent is connected, as the Updates page's machines table counts them "behind".
// Not ok when the machines cannot be read.
func (m *Metrics) machinesBehind(ctx context.Context) (int, bool) {
	d := m.Driver
	nodes, err := d.nodes(ctx)
	if err != nil {
		return 0, false
	}
	wm, err := d.Store.Watermark(ctx)
	if err != nil {
		return 0, false
	}
	if m.Current != nil {
		if cur := m.Current(); cur != "" && ValidVersion(cur) && (wm == "" || CompareVersions(cur, wm) > 0) {
			wm = NormalizeVersion(cur)
		}
	}
	if wm == "" {
		return 0, true
	}
	agents, states := d.agentStates()
	n := 0
	for _, node := range nodes {
		_, online := agents[node.id]
		if !online && !node.full {
			continue // worker-only: not on the cluster's releases
		}
		v := NormalizeVersion(node.version)
		if s, ok := states[node.id]; ok && s.Version != "" {
			v = NormalizeVersion(s.Version)
		}
		if v != "" && ValidVersion(v) && CompareVersions(v, wm) < 0 {
			n++
		}
	}
	return n, true
}
