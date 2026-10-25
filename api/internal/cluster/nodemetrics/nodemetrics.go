// Package nodemetrics is a full node's Prometheus metrics (Epic 35 story 35.15 AC1), on
// the API's /metrics: the sessions it asks the home about (delegation, story 35.9), the
// requests it serves by placement class (story 35.10) and whether the schema guard paused
// it (story 35.8 AC4). They are registered only by a full node's start (Register), so a
// home or a worker-only machine has no such series at all, rather than zeros (EC2).
package nodemetrics

import (
	"strconv"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	metrics "github.com/Hamza-Labs-Core/Maktaba/shared/metrics/go"
)

// Metric names (design §16).
const (
	MetricDelegateRequests  = "maktaba_node_delegate_requests_total"
	MetricDelegateCacheHits = "maktaba_node_delegate_cache_hits_total"
	MetricProxyRequests     = "maktaba_node_proxy_requests_total"
	MetricPaused            = "maktaba_node_paused"
)

// Delegate results: the home said who it is, said no one, or did not answer.
const (
	DelegateOK          = "ok"
	DelegateDenied      = "denied"
	DelegateUnreachable = "unreachable"
)

// Metrics are a node's counters; a nil *Metrics counts nothing.
type Metrics struct {
	delegate  *prometheus.CounterVec
	cacheHits prometheus.Counter
	proxy     *prometheus.CounterVec
}

var (
	once sync.Once
	reg  *Metrics
	// paused is the schema guard's verdict the paused gauge reads (the latest Register's)
	pausedMu sync.Mutex
	paused   func() string
)

// Register registers the node's metrics on the process's registry (once) and returns
// them; pausedFn is the schema guard's verdict ("" = the node's code fits).
func Register(pausedFn func() string) *Metrics {
	pausedMu.Lock()
	paused = pausedFn
	pausedMu.Unlock()
	once.Do(func() {
		reg = New(metrics.Reg())
	})
	return reg
}

// New registers a node's metrics on r (tests use their own registry).
func New(r prometheus.Registerer) *Metrics {
	m := &Metrics{
		delegate: prometheus.NewCounterVec(prometheus.CounterOpts{Name: MetricDelegateRequests,
			Help: "Sessions this node asked its home about, by the answer (ok, denied, unreachable)."}, []string{"result"}),
		cacheHits: prometheus.NewCounter(prometheus.CounterOpts{Name: MetricDelegateCacheHits,
			Help: "Sessions this node answered from its cache of the home's answers."}),
		proxy: prometheus.NewCounterVec(prometheus.CounterOpts{Name: MetricProxyRequests,
			Help: "Requests this node served, by placement class (local, any, home: proxied) and status."}, []string{"class", "status"}),
	}
	r.MustRegister(m.delegate, m.cacheHits, m.proxy, pausedCollector{
		desc: prometheus.NewDesc(MetricPaused, "1 while the schema guard pauses this node's `any` routes, by why.", []string{"reason"}, nil)})
	// every answer reads 0 until it happens, so a rate is never missing
	for _, r := range []string{DelegateOK, DelegateDenied, DelegateUnreachable} {
		m.delegate.WithLabelValues(r)
	}
	return m
}

// Delegate counts one question to the home by its answer.
func (m *Metrics) Delegate(result string) {
	if m != nil {
		m.delegate.WithLabelValues(result).Inc()
	}
}

// CacheHit counts a session answered from the cache.
func (m *Metrics) CacheHit() {
	if m != nil {
		m.cacheHits.Inc()
	}
}

// Served counts a request served by class with its status.
func (m *Metrics) Served(class string, status int) {
	if m != nil {
		m.proxy.WithLabelValues(class, strconv.Itoa(status)).Inc()
	}
}

// pausedCollector reads the guard's verdict at each scrape: {reason="none"} 0 while the
// node's code fits, else {reason=<why>} 1.
type pausedCollector struct{ desc *prometheus.Desc }

func (c pausedCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c pausedCollector) Collect(ch chan<- prometheus.Metric) {
	pausedMu.Lock()
	fn := paused
	pausedMu.Unlock()
	reason := ""
	if fn != nil {
		reason = fn()
	}
	if reason == "" {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, 0, "none")
		return
	}
	ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, 1, PauseCode(reason))
}

// PauseCode is a bounded label for the guard's reason text (schemaguard.PauseReason):
// home_behind (the home's database is older than this machine needs), node_behind (the
// home needs this machine newer), or other.
func PauseCode(reason string) string {
	switch {
	case strings.Contains(reason, "update the home"):
		return "home_behind"
	case strings.Contains(reason, "update this machine"):
		return "node_behind"
	}
	return "other"
}
