// Package metrics implements the cloud relay's aggregate-only
// observability (Epic 30). It collects connection gauges and traffic
// counters in memory, flushes per-minute aggregate rows to Postgres,
// rolls them up hourly, and exposes the live state for Prometheus.
//
// AGGREGATE ONLY: nothing here records a user id, a server id, or an IP
// address. The single dimension is `country`, derived at the edge and
// stored without the IP (Story 30.2). See specs/epics/30-relay-analytics.
package metrics

// Metric names. These are the `metric` column values in
// relay_metrics_raw / relay_metrics_hourly and the basis for the
// Prometheus series names.
const (
	// Gauges — sampled from the live registry each collector tick.
	MetricConnectedServers = "connected_servers"
	MetricActiveTunnels    = "active_tunnels"

	// Counters — accumulated from traffic.
	MetricBandwidthIn  = "bandwidth_in_bytes"
	MetricBandwidthOut = "bandwidth_out_bytes"
	MetricRequests     = "requests" // the only metric dimensioned by country
	MetricPushSent     = "push_sent"
	MetricPushFailed   = "push_failed"
)

// Kind classifies a metric for read-time interpretation and Prometheus
// rendering (counters get the `_total` suffix; gauges do not).
type Kind int

const (
	KindCounter Kind = iota
	KindGauge
)

// KindOf reports whether a metric name is a gauge or a counter. Unknown
// names default to counter.
func KindOf(metric string) Kind {
	switch metric {
	case MetricConnectedServers, MetricActiveTunnels:
		return KindGauge
	default:
		return KindCounter
	}
}
