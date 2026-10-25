package updates

// Story 35.15 AC1: the update flow's Prometheus metrics, on the API's /metrics (admin
// port). Every number is read from update_runs, update_run_steps, update_targets and the
// catalog when Prometheus scrapes (at most every metricsCacheFor), not counted in memory:
// an API that restarts mid-run (an update restarts it) shows the same counts after as
// before (EC1). The supervisor's own (the database step's migration time) are its
// /metrics, merged into the API's (cmd/maktaba-server/internal/updater/metrics.go).
//
// Labels are bounded: run and target states, step names, verification failure kinds.
// Never a run id, a node id or a version.

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	metrics "github.com/Hamza-Labs-Core/Maktaba/shared/metrics/go"
)

// Metric names (design §16).
const (
	MetricRuns            = "maktaba_update_runs_total"
	MetricRunDuration     = "maktaba_update_run_duration_seconds"
	MetricStepDuration    = "maktaba_update_step_duration_seconds"
	MetricBackupBytes     = "maktaba_update_backup_bytes"
	MetricTargets         = "maktaba_update_targets"
	MetricNodeApply       = "maktaba_update_node_apply_duration_seconds"
	MetricVerifyFailures  = "maktaba_update_verify_failures_total"
	MetricMachinesBehind  = "maktaba_update_machines_behind"
	MetricLastCheck       = "maktaba_update_last_check_timestamp"
	metricsCacheFor       = 15 * time.Second
	metricsQueryTimeout   = 5 * time.Second
	metricsMaxDurationRow = 5000 // the newest rows a histogram is built from
)

// Histogram buckets (seconds): a run is minutes to hours; a step seconds to an hour; a
// machine's apply (its switch until it is healthy) about a minute to an hour.
var (
	runBuckets   = []float64{60, 300, 600, 1800, 3600, 7200, 14400, 28800}
	stepBuckets  = []float64{1, 5, 15, 60, 300, 900, 1800, 3600}
	applyBuckets = []float64{30, 60, 120, 300, 600, 1200, 1800, 3600}
)

// targetStates are every target state, so a state that empties reads 0.
var targetStates = []string{TargetPending, TargetOffline, TargetPreparing, TargetPrepared, TargetFailedPrepare,
	TargetApplying, TargetHealthy, TargetRolledBack, TargetFailed, TargetHeld, TargetSkipped, TargetWorkerOnly,
	TargetWaitingClick, TargetClicked, TargetNeedsPerson}

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
	runDur    []float64
	stepDur   map[string][]float64
	apply     []float64
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
	ch <- constHistogram(m.descRunDur, runBuckets, s.runDur)
	for step, vals := range s.stepDur {
		ch <- constHistogram(m.descStepDur, stepBuckets, vals, step)
	}
	if s.backup != nil {
		ch <- prometheus.MustNewConstMetric(m.descBackup, prometheus.GaugeValue, *s.backup)
	}
	for _, state := range targetStates {
		ch <- prometheus.MustNewConstMetric(m.descTargets, prometheus.GaugeValue, s.targets[state], state)
	}
	ch <- constHistogram(m.descApply, applyBuckets, s.apply)
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

// constHistogram is a histogram of vals (seconds) over buckets.
func constHistogram(desc *prometheus.Desc, buckets, vals []float64, labels ...string) prometheus.Metric {
	counts := make(map[float64]uint64, len(buckets))
	var sum float64
	for _, v := range vals {
		sum += v
		for _, b := range buckets {
			if v <= b {
				counts[b]++
			}
		}
	}
	return prometheus.MustNewConstHistogram(desc, uint64(len(vals)), sum, counts, labels...)
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
	s := &metricsSnap{at: now, runs: map[string]float64{}, stepDur: map[string][]float64{},
		targets: map[string]float64{}, verify: map[string]float64{}}
	st := m.Driver.Store
	db := st.DB
	if rows, err := db.QueryContext(ctx, `SELECT state, started_at, finished_at FROM update_runs
		WHERE finished_at IS NOT NULL ORDER BY started_at DESC`); err == nil {
		n := 0
		for rows.Next() {
			var state string
			var started, finished nullTime
			if rows.Scan(&state, &started, &finished) != nil {
				continue
			}
			s.runs[state]++
			if n++; n <= metricsMaxDurationRow && started.t != nil && finished.t != nil {
				s.runDur = append(s.runDur, finished.t.Sub(*started.t).Seconds())
			}
		}
		_ = rows.Close()
	}
	if rows, err := db.QueryContext(ctx, `SELECT step, started_at, finished_at FROM update_run_steps
		WHERE finished_at IS NOT NULL AND state IN ('done', 'failed') ORDER BY started_at DESC`); err == nil {
		n := 0
		for rows.Next() {
			var step string
			var started, finished nullTime
			if rows.Scan(&step, &started, &finished) != nil || started.t == nil || finished.t == nil {
				continue
			}
			if n++; n <= metricsMaxDurationRow {
				s.stepDur[step] = append(s.stepDur[step], finished.t.Sub(*started.t).Seconds())
			}
		}
		_ = rows.Close()
	}
	if rows, err := db.QueryContext(ctx, `SELECT applied_at, healthy_at FROM update_targets
		WHERE applied_at IS NOT NULL AND healthy_at IS NOT NULL ORDER BY healthy_at DESC`); err == nil {
		for rows.Next() {
			var applied, healthy nullTime
			if rows.Scan(&applied, &healthy) != nil || applied.t == nil || healthy.t == nil {
				continue
			}
			if len(s.apply) < metricsMaxDurationRow {
				s.apply = append(s.apply, healthy.t.Sub(*applied.t).Seconds())
			}
		}
		_ = rows.Close()
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
	if cat, cerr := st.Catalog(ctx); cerr == nil {
		for _, r := range cat {
			if r.VerifyError != nil {
				kind := r.VerifyError.Kind
				if kind == "" {
					kind = "unknown"
				}
				s.verify[kind]++
			}
		}
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
