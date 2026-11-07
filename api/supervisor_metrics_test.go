package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
	metrics "github.com/Hamza-Labs-Core/Maktaba/shared/metrics/go"
)

// The API's /metrics carries this machine's supervisor's update metrics (one scrape per
// machine), and only those; a supervisor that does not answer adds nothing.
func TestMetricsCarryTheSupervisors(t *testing.T) {
	sup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "# HELP maktaba_update_migration_duration_seconds x\n"+
			"# TYPE maktaba_update_migration_duration_seconds histogram\n"+
			"maktaba_update_migration_duration_seconds_bucket{le=\"60\"} 1\n"+
			"maktaba_update_migration_duration_seconds_bucket{le=\"+Inf\"} 1\n"+
			"maktaba_update_migration_duration_seconds_sum 12\n"+
			"maktaba_update_migration_duration_seconds_count 1\n"+
			// story 51.14: the machine's components
			"# TYPE maktaba_component_up gauge\n"+
			"maktaba_component_up{id=\"youtube\",machine=\"home\",phase=\"off\"} 0\n"+
			// story 53.12: the machine's memory, disk and load
			"# TYPE maktaba_host_load1 gauge\nmaktaba_host_load1{machine=\"home\"} 1.5\n"+
			// a family the API has of its own too (taken, it would repeat the
			// API's) and one only the supervisor has: neither is taken
			"# TYPE go_goroutines gauge\ngo_goroutines 7\n"+
			"# TYPE maktaba_supervisor_children gauge\nmaktaba_supervisor_children 4\n")
	}))
	defer sup.Close()
	scrape := func(url string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		metricsHandler(url).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	body := scrape(sup.URL)
	if !strings.Contains(body, "maktaba_update_migration_duration_seconds_count 1") {
		t.Fatalf("the supervisor's migrations are missing:\n%s", body)
	}
	if !strings.Contains(body, `maktaba_component_up{id="youtube",machine="home",phase="off"} 0`) {
		t.Fatalf("the supervisor's components are missing:\n%s", body)
	}
	if !strings.Contains(body, `maktaba_host_load1{machine="home"} 1.5`) {
		t.Fatalf("the machine's load is missing:\n%s", body)
	}
	// go_goroutines is the API's own family too (its value may well be 7):
	// the check is that the family appears once, and the supervisor-only one not at all
	if strings.Contains(body, "maktaba_supervisor_children") || strings.Count(body, "# TYPE go_goroutines ") > 1 {
		t.Fatal("a family of the supervisor's other than the update metrics was taken")
	}
	sup.Close()
	if body := scrape(sup.URL); strings.Contains(body, "maktaba_update_migration_duration_seconds") {
		t.Fatal("a supervisor that does not answer still showed")
	}
}

// The maktaba_* families on prometheus' default registry (the plugins' metrics, Epic 54)
// show on /metrics too, once even when the process registry has the same family, and its
// Go and process families are not repeated. The queue's series (Epic 53 story 53.12) show
// once the home registers them.
func TestMetricsCarryTheDefaultRegistrysAndTheQueues(t *testing.T) {
	onlyDefault := prometheus.NewGauge(prometheus.GaugeOpts{Name: "maktaba_test_default_only", Help: "x"})
	onlyDefault.Set(3)
	prometheus.MustRegister(onlyDefault)
	both := prometheus.NewGauge(prometheus.GaugeOpts{Name: "maktaba_test_both", Help: "x"})
	prometheus.MustRegister(both)
	metrics.MustRegister(prometheus.NewGauge(prometheus.GaugeOpts{Name: "maktaba_test_both", Help: "x"}))
	queues.RegisterMetrics(queuestest.New().DB)

	rec := httptest.NewRecorder()
	metricsHandler("").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, body)
	}
	if !strings.Contains(body, "maktaba_test_default_only 3") {
		t.Fatalf("the default registry's family is missing:\n%s", body)
	}
	for _, fam := range []string{"maktaba_test_both", "go_goroutines", "process_cpu_seconds_total"} {
		if n := strings.Count(body, "# TYPE "+fam+" "); n != 1 {
			t.Errorf("%s: %d families", fam, n)
		}
	}
	if !strings.Contains(body, "maktaba_pipeline_audio_seconds_last_hour 0") {
		t.Errorf("the queue's series are missing:\n%s", body)
	}
}
