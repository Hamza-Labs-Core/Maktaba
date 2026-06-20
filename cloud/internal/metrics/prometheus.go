package metrics

import (
	"fmt"
	"io"
	"net/http"
)

// Prometheus series metadata. Counter names carry the `_total` suffix per
// OpenMetrics convention; gauges do not.
type promSeries struct {
	name string
	help string
	kind Kind
	val  func(PromSnapshot) int64
}

var promSeriesList = []promSeries{
	{"maktaba_relay_connected_servers", "Currently connected home servers.", KindGauge,
		func(s PromSnapshot) int64 { return s.ConnectedServers }},
	{"maktaba_relay_active_tunnels", "Currently active relay tunnels.", KindGauge,
		func(s PromSnapshot) int64 { return s.ActiveTunnels }},
	{"maktaba_relay_bandwidth_in_bytes_total", "Total bytes proxied client→server.", KindCounter,
		func(s PromSnapshot) int64 { return s.BandwidthIn }},
	{"maktaba_relay_bandwidth_out_bytes_total", "Total bytes proxied server→client.", KindCounter,
		func(s PromSnapshot) int64 { return s.BandwidthOut }},
	{"maktaba_relay_requests_total", "Total proxied requests.", KindCounter,
		func(s PromSnapshot) int64 { return s.Requests }},
	{"maktaba_relay_push_sent_total", "Total push notifications delivered.", KindCounter,
		func(s PromSnapshot) int64 { return s.PushSent }},
	{"maktaba_relay_push_failed_total", "Total push notifications that failed.", KindCounter,
		func(s PromSnapshot) int64 { return s.PushFailed }},
}

func kindString(k Kind) string {
	if k == KindGauge {
		return "gauge"
	}
	return "counter"
}

// RenderPrometheus writes the snapshot as Prometheus text exposition
// (version 0.0.4 / OpenMetrics-compatible). Each series emits HELP, TYPE,
// and a single value line.
func RenderPrometheus(w io.Writer, s PromSnapshot) error {
	for _, ser := range promSeriesList {
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n", ser.name, ser.help); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "# TYPE %s %s\n", ser.name, kindString(ser.kind)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%s %d\n", ser.name, ser.val(s)); err != nil {
			return err
		}
	}
	return nil
}

// PrometheusHandler serves GET /metrics from the live collector. No auth:
// it is scraped on the internal network (Story 30.4).
func PrometheusHandler(c *Collector) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_ = RenderPrometheus(w, c.PromSnapshot())
	}
}
