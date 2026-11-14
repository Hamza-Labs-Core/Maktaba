package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Story 50.28: the private listener's handler answers /metrics with the private series and
// passes the probes to the role's router; nothing else is served there.
func TestPrivateMetricsHandler(t *testing.T) {
	probes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("probe " + r.URL.Path)) })
	h := privateMetrics(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("private series")) }, probes)
	for path, want := range map[string]string{"/metrics": "private series", "/readyz": "probe /readyz", "/healthz": "probe /healthz"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
	for _, path := range []string{"/", "/v1/broadcast/analytics", "/privacy"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, rec.Code)
		}
	}
}
