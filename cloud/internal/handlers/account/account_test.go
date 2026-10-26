package account

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// The email-change route was a 202 no-op (stub audit #23): with no mail
// provider there is no verification step, so the route is not mounted at
// all and a caller gets a plain "no such route" instead of a false
// success. The other account routes stay.
func TestEmailChangeRouteNotMounted(t *testing.T) {
	r := chi.NewRouter()
	Mount(r, &Deps{})
	req := httptest.NewRequest(http.MethodPost, "/v1/account/email", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code == http.StatusAccepted || (rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed) {
		t.Fatalf("POST /v1/account/email = %d, want 404/405", rec.Code)
	}
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/v1/account/me"},
		{http.MethodPatch, "/v1/account/me"},
		{http.MethodPost, "/v1/account/password"},
		{http.MethodDelete, "/v1/account/me"},
	} {
		if !r.Match(chi.NewRouteContext(), rt.method, rt.path) {
			t.Errorf("%s %s not mounted", rt.method, rt.path)
		}
	}
}
