package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
			"# TYPE go_goroutines gauge\ngo_goroutines 7\n")
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
	if strings.Contains(body, "go_goroutines 7") {
		t.Fatal("a family of the supervisor's other than the update metrics was taken")
	}
	sup.Close()
	if body := scrape(sup.URL); strings.Contains(body, "maktaba_update_migration_duration_seconds") {
		t.Fatal("a supervisor that does not answer still showed")
	}
}
