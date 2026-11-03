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
	with := get(metricsAdmin(base, dir, &supervisor.ComponentMetrics{Source: metricsRows{}, Machine: "home"}))
	if !strings.Contains(with, "maktaba_update_") ||
		!strings.Contains(with, `maktaba_component_up{id="youtube",machine="home",phase="off"} 0`) {
		t.Fatalf("metrics:\n%s", with)
	}
	if without := get(metricsAdmin(base, dir, nil)); strings.Contains(without, "maktaba_component_up{") {
		t.Fatalf("components without a reconciler:\n%s", without)
	}
}
