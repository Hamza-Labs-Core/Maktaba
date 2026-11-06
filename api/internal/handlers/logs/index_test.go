package logs

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// Epic 53 story 53.09 T05: the log index's routes, as nobody (401), a viewer (403) and an
// admin (the role's answer, its query passed on); with no role, 503.
func TestIndexRoutesRoles(t *testing.T) {
	var seen string
	role := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"lines":[]}`)
	}))
	defer role.Close()
	h := &Handler{IndexAddr: strings.TrimPrefix(role.URL, "http://")}
	r := chi.NewRouter()
	h.Mount(r)
	call := func(path string, p *principal.Principal) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, p := range []string{"/api/admin/logs/search?q=boom", "/api/admin/logs/around?id=3", "/api/admin/logs/tail"} {
		if c := call(p, nil); c != http.StatusUnauthorized {
			t.Errorf("%s as nobody: %d", p, c)
		}
		if c := call(p, &principal.Principal{UserID: "v", IsAdmin: false}); c != http.StatusForbidden {
			t.Errorf("%s as a viewer: %d", p, c)
		}
	}
	if c := call("/api/admin/logs/search?q=boom&level=error", &principal.Principal{UserID: "a", IsAdmin: true}); c != 200 ||
		seen != "/search?q=boom&level=error" {
		t.Errorf("as an admin: %d, the role saw %q", c, seen)
	}
	h.IndexAddr = "127.0.0.1:1"
	if c := call("/api/admin/logs/search", &principal.Principal{UserID: "a", IsAdmin: true}); c != http.StatusServiceUnavailable {
		t.Errorf("no role answering: %d, want 503", c)
	}
}
