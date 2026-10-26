package analytics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Epic 16 story 16.2: on Free the dashboard covers 30 days; longer
// ranges, the per-user breakdown and export are refused with a plain
// reason, before any query runs (no DB here).
func TestAnalytics_FreeTierGate(t *testing.T) {
	h := &Handler{Advanced: func() (bool, string) { return false, "free" }}
	r := chi.NewRouter()
	h.Mount(r)
	ctx := principal.WithPrincipal(context.Background(), &principal.Principal{UserID: "a", IsAdmin: true})
	for _, path := range []string{
		"/api/admin/analytics/summary?range=90d", "/api/admin/analytics/top-videos?range=1y",
		"/api/admin/analytics/activity?range=all", "/api/admin/analytics/users", "/api/admin/analytics/export",
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "license-required") ||
			!strings.Contains(rec.Body.String(), "older than 30 days") {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	// a viewer is refused as before (the role gate runs first)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/analytics/users", nil).
		WithContext(principal.WithPrincipal(context.Background(), &principal.Principal{UserID: "v"})))
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "license-required") {
		t.Errorf("viewer: %d %s", rec.Code, rec.Body.String())
	}
}
