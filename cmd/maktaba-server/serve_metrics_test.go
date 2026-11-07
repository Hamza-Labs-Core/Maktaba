package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

type metricsRows struct{}

func (metricsRows) StatusesOfMachine(context.Context, string) ([]store.Status, error) {
	return []store.Status{{Machine: "home", ID: "youtube", Part: "pipeline", Phase: "off"}}, nil
}

func (metricsRows) Desired(context.Context, string) (map[string]store.Desired, int64, error) {
	return nil, 0, nil
}

// Story 51.14: the supervisor's /metrics has the update metrics and this machine's
// components beside them; with no reconciler, only the update metrics.
func TestMetricsAdminComponents(t *testing.T) {
	dir := t.TempDir()
	get := func(h http.Handler) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		return rec.Body.String()
	}
	base := http.NotFoundHandler()
	with := get(metricsAdmin(base, dir, &supervisor.ComponentMetrics{Source: metricsRows{}, Machine: "home"}, nil))
	if !strings.Contains(with, "maktaba_update_") ||
		!strings.Contains(with, `maktaba_component_up{id="youtube",machine="home",phase="off"} 0`) {
		t.Fatalf("metrics:\n%s", with)
	}
	if without := get(metricsAdmin(base, dir, nil, nil)); strings.Contains(without, "maktaba_component_up{") {
		t.Fatalf("components without a reconciler:\n%s", without)
	}
}

// Story 53.12: the supervisor's /metrics has this machine's memory, disk free and load,
// labelled with the machine; a reading the system lacks is left out, not written as 0.
func TestMetricsAdminHost(t *testing.T) {
	host := &hostMetrics{DataDir: "/data", Total: func() uint64 { return 16 << 30 }, Free: func() uint64 { return 0 },
		Disk: func(p string) uint64 {
			if p != "/data" {
				t.Errorf("disk of %q", p)
			}
			return 200 << 30
		}, Load: func() (float64, bool) { return 1.5, true }, CPUs: 8}
	rec := httptest.NewRecorder()
	h := metricsAdmin(http.NotFoundHandler(), t.TempDir(), &supervisor.ComponentMetrics{Source: metricsRows{}, Machine: "box2"}, host)
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{`maktaba_host_memory_bytes{machine="box2"} 1.7179869184e+10`,
		`maktaba_host_disk_free_bytes{machine="box2"} 2.147483648e+11`, `maktaba_host_load1{machine="box2"} 1.5`,
		`maktaba_host_cpus{machine="box2"} 8`} {
		if !strings.Contains(body, want) {
			t.Errorf("no %s in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "maktaba_host_memory_free_bytes") {
		t.Error("an unknown free memory was written")
	}
	host.Load = func() (float64, bool) { return 0, false }
	rec = httptest.NewRecorder()
	metricsAdmin(http.NotFoundHandler(), t.TempDir(), nil, host).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if b := rec.Body.String(); strings.Contains(b, "maktaba_host_load1") || !strings.Contains(b, `maktaba_host_cpus{machine="home"} 8`) {
		t.Errorf("without a load average, or a reconciler:\n%s", b)
	}
}
