package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// Epic 53 story 53.16: MountLogs mounts the problem reports' Maktaba routes and answers the
// forwarder main declares as api:reports_forwarder.
func TestMountLogsAnswersTheReportsForwarder(t *testing.T) {
	r := chi.NewRouter()
	fw := MountLogs(r, LogsDeps{})
	if fw == nil {
		t.Fatal("no forwarder")
	}
	mounted := map[string]bool{}
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		mounted[method+" "+route] = true
		return nil
	})
	for _, rt := range []string{"GET /api/reports/maktaba", "PUT /api/reports/maktaba", "DELETE /api/reports/maktaba",
		"POST /api/reports/{id}/share", "GET /api/reports/{id}/maktaba-preview", "GET /api/reports/{id}/screenshot"} {
		if !mounted[rt] {
			t.Errorf("%s not mounted", rt)
		}
	}
	// nobody signed in: refused like every report route
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/reports/maktaba", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}
}
