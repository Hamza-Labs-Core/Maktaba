package plugins

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	pl "github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
)

// adminRoutes are every route of Admin.Mount (method and path, a parameter filled).
var adminRoutes = []string{
	"GET /api/plugins",
	"GET /api/plugins/policy",
	"PUT /api/plugins/policy",
	"POST /api/plugins/install",
	"GET /api/plugins/dev.example.x",
	"PUT /api/plugins/dev.example.x/grants",
	"POST /api/plugins/dev.example.x/enable",
	"POST /api/plugins/dev.example.x/disable",
	"POST /api/plugins/dev.example.x/start",
	"POST /api/plugins/dev.example.x/stop",
	"POST /api/plugins/dev.example.x/update",
	"POST /api/plugins/dev.example.x/rollback",
	"DELETE /api/plugins/dev.example.x",
	"GET /api/plugins/dev.example.x/writes",
	"POST /api/plugins/dev.example.x/writes/revert",
	"GET /api/plugins/dev.example.x/settings",
	"PUT /api/plugins/dev.example.x/settings",
}

// Story 54.08 T01 (the refusals): every route answers no one 401, a viewer 403 and a
// cloud-linked play sign-in 403 (even one whose home user is an admin), before any handler
// runs: nothing is read or written for them. The admin's 2xx is the Postgres tests'.
func TestEveryPluginRouteIsAdminOnly(t *testing.T) {
	r := chi.NewRouter()
	h := &Admin{L: &pl.Lifecycle{}}
	h.Mount(r)
	mounted := 0
	_ = chi.Walk(r, func(_, _ string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		mounted++
		return nil
	})
	if mounted != len(adminRoutes) {
		t.Fatalf("Mount has %d routes, the test %d: add the new one to adminRoutes", mounted, len(adminRoutes))
	}
	// a cloud-linked play sign-in of a user who is an admin at home
	play := &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000b1", IsAdmin: true}
	play.ApplyCloudScope(principal.CloudScopePlay, nil)
	for _, rt := range adminRoutes {
		method, path, _ := strings.Cut(rt, " ")
		for _, who := range []struct {
			name string
			p    *principal.Principal
			want int
		}{
			{"no one", nil, http.StatusUnauthorized},
			{"viewer", &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000c1"}, http.StatusForbidden},
			{"cloud play", play, http.StatusForbidden},
		} {
			req := httptest.NewRequest(method, path, strings.NewReader("{}"))
			if who.p != nil {
				req = req.WithContext(principal.WithPrincipal(req.Context(), who.p))
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != who.want {
				t.Errorf("%s as %s: %d, want %d", rt, who.name, rec.Code, who.want)
			}
		}
	}
}
