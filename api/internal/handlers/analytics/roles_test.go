package analytics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// TestAdminRoutesRefuseNonAdmins: the API enforces roles. Every analytics
// route shows other people's watching and the apps' use, so anonymous
// callers and viewers get 403 before anything is read (the handler has no
// database here, so a query would panic), with or without filters.
func TestAdminRoutesRefuseNonAdmins(t *testing.T) {
	r := chi.NewRouter()
	(&Handler{}).Mount(r)
	viewer := &principal.Principal{UserID: uOmar}
	for _, path := range []string{
		"summary", "now", "activity", "top-videos", "top-channels", "libraries", "genres",
		"people", "apps", "remote-tv", "export", "export?format=json",
		"summary?range=30d&surface=tuner", "people?user=" + uOmar, "apps?from=nope",
	} {
		for _, p := range []*principal.Principal{nil, viewer} {
			req := httptest.NewRequest("GET", "/api/admin/analytics/"+path, nil)
			if p != nil {
				req = req.WithContext(principal.WithPrincipal(req.Context(), p))
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("GET %s as %v: %d, want 403", path, p, rec.Code)
			}
		}
	}
}

// TestBadFiltersAre400: an admin's bad filter is refused before any read.
func TestBadFiltersAre400(t *testing.T) {
	r := chi.NewRouter()
	(&Handler{}).Mount(r)
	admin := &principal.Principal{UserID: uSara, IsAdmin: true}
	for _, path := range []string{
		"summary?surface=plex", "now?user=me", "activity?from=2026-10-05&to=2026-10-01",
		"top-channels?library=x", "apps?client_app=Bad%20App", "export?format=xml",
		"export?to=never", "people?limit=many",
	} {
		req := httptest.NewRequest("GET", "/api/admin/analytics/"+path, nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), admin))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s: %d, want 400", path, rec.Code)
		}
	}
}
