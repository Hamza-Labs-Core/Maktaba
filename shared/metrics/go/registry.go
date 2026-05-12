// Package metrics is the shared Prometheus surface for Maktaba's Go
// services (Story 21.2). One *prometheus.Registry per process; all
// services register their counters/gauges against the same registry
// and the /metrics handler exposes whatever has been registered.
//
// Cardinality discipline: never include a per-row id (`video_id`,
// `user_id`, `session_id`, `path`) in a label. The cardinality lint
// (Story 21.2 AC-2) enforces this against registration sites.
package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

var (
	regOnce sync.Once
	reg     *prometheus.Registry
)

// Reg returns the process-wide registry, constructing it on first
// call. Test code that needs an isolated registry should construct its
// own *prometheus.Registry rather than mutating this one.
func Reg() *prometheus.Registry {
	regOnce.Do(func() {
		reg = prometheus.NewRegistry()
		// Wire in process / Go runtime collectors so /metrics exposes
		// rss / open fds / goroutines without each service having to
		// remember.
		reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
		reg.MustRegister(collectors.NewGoCollector())
	})
	return reg
}

// FixedMSBuckets is the documented fallback histogram bucket layout
// when native histograms are unavailable (Story 21.2 AC-3). Values
// are seconds; convert from ms in NewLatencyHistogram.
var FixedMSBuckets = []float64{1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000}

func msToS(ms []float64) []float64 {
	out := make([]float64, len(ms))
	for i, v := range ms {
		out[i] = v / 1000.0
	}
	return out
}

// NewLatencyHistogram registers a *_seconds histogram with both native
// and fallback buckets so /metrics works against new and old Prometheus
// versions. The name is given without the `_seconds` suffix; the
// helper appends it so all latency metrics share the same convention.
func NewLatencyHistogram(name, help string, labels []string) *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:                            name + "_seconds",
		Help:                            help,
		NativeHistogramBucketFactor:     1.1,
		NativeHistogramMaxBucketNumber:  100,
		NativeHistogramMinResetDuration: 0,
		Buckets:                         msToS(FixedMSBuckets),
	}, labels)
	Reg().MustRegister(h)
	return h
}

// MustRegister adds collectors to the shared registry. Wraps
// reg.MustRegister so callers don't have to import the prometheus
// package in addition to this one.
func MustRegister(c ...prometheus.Collector) {
	Reg().MustRegister(c...)
}
