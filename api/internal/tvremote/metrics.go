package tvremote

// Story 32.11: the remote controller's Prometheus metrics, on the API's
// existing /metrics (admin port). Labels are bounded and never carry a user
// name or an IP address (AC1): channel ids, push states, ERR_* codes and the
// billing month.

import (
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	metrics "github.com/Hamza-Labs-Core/Maktaba/shared/metrics/go"
)

// Metric names (design §10).
const (
	MetricRemoteViewers    = "maktaba_tv_remote_viewers"
	MetricRemotePushes     = "maktaba_tv_remote_pushes"
	MetricRemotePushErrors = "maktaba_tv_remote_push_errors_total"
	MetricRemoteMinutes    = "maktaba_tv_remote_minutes_delivered"
	MetricRemoteCost       = "maktaba_tv_remote_cost_usd"
)

// remoteMetrics are registered once per process (the shared registry).
type remoteMetrics struct {
	viewers    *prometheus.GaugeVec
	pushes     *prometheus.GaugeVec
	pushErrors *prometheus.CounterVec
	minutes    *prometheus.GaugeVec
	cost       *prometheus.GaugeVec
}

var (
	metricsOnce sync.Once
	rm          *remoteMetrics
)

// rmetrics returns the remote metrics, registering them on first use.
func rmetrics() *remoteMetrics {
	metricsOnce.Do(func() {
		rm = &remoteMetrics{
			viewers: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: MetricRemoteViewers,
				Help: "Remote viewers (open sessions) per channel."}, []string{"channel"}),
			pushes: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: MetricRemotePushes,
				Help: "Cloudflare pushes by state."}, []string{"state"}),
			pushErrors: prometheus.NewCounterVec(prometheus.CounterOpts{Name: MetricRemotePushErrors,
				Help: "Failed or retried pushes by code (Cloudflare ERR_* or push_failed / push_retried)."}, []string{"code"}),
			minutes: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: MetricRemoteMinutes,
				Help: "Minutes delivered by Cloudflare this month (analytics, else sessions)."}, []string{"month"}),
			cost: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: MetricRemoteCost,
				Help: "Estimated Cloudflare cost this month (USD)."}, []string{"month"}),
		}
		metrics.MustRegister(rm.viewers, rm.pushes, rm.pushErrors, rm.minutes, rm.cost)
	})
	return rm
}

// pushStates are every push state, so a state that empties reads 0.
var pushStates = []string{PushRequested, PushConnecting, PushLive, PushStopping, PushCleanup}

// observe sets the gauges from one sample.
func (m *remoteMetrics) observe(viewers map[string]int, pushes map[string]int, month string, minutes, cost float64) {
	m.viewers.Reset()
	for ch, n := range viewers {
		m.viewers.WithLabelValues(ch).Set(float64(n))
	}
	for _, s := range pushStates {
		m.pushes.WithLabelValues(s).Set(float64(pushes[s]))
	}
	if month != "" {
		m.minutes.Reset()
		m.cost.Reset()
		m.minutes.WithLabelValues(month).Set(minutes)
		m.cost.WithLabelValues(month).Set(cost)
	}
}

// pushError counts a failed or retried push.
func (m *remoteMetrics) pushError(code string) { m.pushErrors.WithLabelValues(code).Inc() }

// ErrCode is the bounded code of a push failure: the Cloudflare ERR_* code in
// why, else fallback.
func ErrCode(why, fallback string) string {
	if i := strings.Index(why, "ERR_"); i >= 0 {
		end := i + 4
		for end < len(why) && (why[end] == '_' || (why[end] >= 'A' && why[end] <= 'Z')) {
			end++
		}
		return why[i:end]
	}
	return fallback
}
