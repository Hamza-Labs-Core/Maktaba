package libraries

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// TestMutationsRefuseNonAdmins: the API enforces roles. Creating, changing, deleting and
// scanning a library are the home admins' (the web's Settings, the Remote apps): anonymous
// callers and viewers, even one the library is shared with, get 403 before any query runs
// (the handler has no database here, so one would panic).
func TestMutationsRefuseNonAdmins(t *testing.T) {
	const lib = "6f1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a4b"
	r := chi.NewRouter()
	h := &Handler{}
	h.Mount(r)
	h.MountAudit(r)
	viewer := &principal.Principal{UserID: "u2", Libraries: []string{lib}}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/libraries", `{"name":"Talks","roots":["/media/talks"]}`},
		{"PATCH", "/api/libraries/" + lib, `{"name":"Renamed"}`},
		{"DELETE", "/api/libraries/" + lib, ""},
		{"DELETE", "/api/libraries/" + lib + "?purge=true&confirm=Talks", ""},
		{"POST", "/api/libraries/" + lib + "/scan", ""},
		{"GET", "/api/libraries/" + lib + "/audit", ""},
		// epic 45: the Manage libraries page (manage and folders also prove the
		// static segments win over {id}: Get would answer a viewer 400)
		{"GET", "/api/libraries/manage", ""},
		{"GET", "/api/libraries/folders", ""},
		{"GET", "/api/libraries/folders?path=/", ""},
		{"GET", "/api/libraries/" + lib + "/impact", ""},
		{"POST", "/api/libraries/" + lib + "/merge", `{"into":"` + lib + `","confirm":"Talks"}`},
		{"POST", "/api/libraries/" + lib + "/empty-trash", ""},
	} {
		for _, p := range []*principal.Principal{nil, viewer} {
			req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			if p != nil {
				req = req.WithContext(principal.WithPrincipal(req.Context(), p))
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %v: %d, want 403", c.method, c.path, p, rec.Code)
			}
		}
	}
}
