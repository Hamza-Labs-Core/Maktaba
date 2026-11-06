package metrics

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// The relay's extra series (story 50.03's tunnel counters) follow the
// collector's on /metrics.
func TestPrometheusHandlerExtraSeries(t *testing.T) {
	h := PrometheusHandler(NewCollector(), func(w io.Writer) error {
		_, err := io.WriteString(w, "maktaba_relay_tunnel_drain_forced_total 3\n")
		return err
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "maktaba_relay_connected_servers") || !strings.HasSuffix(body, "maktaba_relay_tunnel_drain_forced_total 3\n") {
		t.Errorf("body:\n%s", body)
	}
}
