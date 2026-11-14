package broadcastaccess

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// agent is a fake relay agent: it records each call and answers 201 with a body.
func agent(t *testing.T) (addr string, seen *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls = append(calls, r.Method+" "+r.URL.Path+" "+string(b))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), &calls
}

// as serves the routes to a request made by p (nil: no sign-in).
func as(h *Handler, p *principal.Principal, method, path, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if p != nil {
				req = req.WithContext(principal.WithPrincipal(req.Context(), p))
			}
			next.ServeHTTP(w, req)
		})
	})
	h.Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

var routes = []struct{ method, path, body, agent string }{
	{http.MethodGet, Prefix, "", "GET /admin/cloud-link/broadcast/access "},
	{http.MethodPut, Prefix + "/channels/news", `{"access":"invite"}`, `PUT /admin/cloud-link/broadcast/channels/news/access {"access":"invite"}`},
	{http.MethodPut, Prefix + "/limits", `{"max_sessions_per_ip":3}`, `PUT /admin/cloud-link/broadcast/viewer-limits {"max_sessions_per_ip":3}`},
	{http.MethodGet, Prefix + "/invites", "", "GET /admin/cloud-link/broadcast/invites "},
	{http.MethodPost, Prefix + "/invites", `{"label":"Grandma"}`, `POST /admin/cloud-link/broadcast/invites {"label":"Grandma"}`},
	{http.MethodDelete, Prefix + "/invites/i1", "", "DELETE /admin/cloud-link/broadcast/invites/i1 "},
	{http.MethodGet, Prefix + "/invites/i1/devices", "", "GET /admin/cloud-link/broadcast/invites/i1/devices "},
	{http.MethodDelete, Prefix + "/invites/i1/devices/p1", "", "DELETE /admin/cloud-link/broadcast/invites/i1/devices/p1 "},
}

// Role test (story 50.26): an owner or admin reaches every route; the agent sees the call.
func TestAnAdminManagesViewerAccess(t *testing.T) {
	addr, seen := agent(t)
	h := &Handler{AgentAddr: addr}
	admin := &principal.Principal{UserID: "u1", IsAdmin: true}
	for _, c := range routes {
		rec := as(h, admin, c.method, c.path, c.body)
		if rec.Code != http.StatusCreated || rec.Body.String() != `{"ok":true}` {
			t.Fatalf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
		if got := (*seen)[len(*seen)-1]; got != c.agent {
			t.Fatalf("the agent saw %q, want %q", got, c.agent)
		}
	}
	// a cloud sign-in with the manage scope is the admin's full role
	manage := &principal.Principal{UserID: "u1", IsAdmin: true}
	manage.ApplyCloudScope(principal.CloudScopeManage, nil)
	if rec := as(h, manage, http.MethodPost, Prefix+"/invites", `{}`); rec.Code != http.StatusCreated {
		t.Fatalf("manage scope: %d", rec.Code)
	}
}

// Role test (story 50.26): a member, an admin's play-scope cloud sign-in and no sign-in reach
// none of the routes, and nothing reaches the agent.
func TestOnlyAnOwnerOrAdminManagesViewerAccess(t *testing.T) {
	addr, seen := agent(t)
	h := &Handler{AgentAddr: addr}
	member := &principal.Principal{UserID: "u2"}
	play := &principal.Principal{UserID: "u1", IsAdmin: true}
	play.ApplyCloudScope(principal.CloudScopePlay, nil)
	for _, c := range routes {
		if rec := as(h, member, c.method, c.path, c.body); rec.Code != http.StatusForbidden {
			t.Fatalf("member %s %s: %d", c.method, c.path, rec.Code)
		}
		if rec := as(h, play, c.method, c.path, c.body); rec.Code != http.StatusForbidden {
			t.Fatalf("play scope %s %s: %d", c.method, c.path, rec.Code)
		}
		if rec := as(h, nil, c.method, c.path, c.body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("no sign-in %s %s: %d", c.method, c.path, rec.Code)
		}
	}
	if len(*seen) != 0 {
		t.Fatalf("the agent saw %q", *seen)
	}
}

// An id that is not one segment of id characters never reaches the agent.
func TestABadIDIsRefused(t *testing.T) {
	addr, seen := agent(t)
	h := &Handler{AgentAddr: addr}
	admin := &principal.Principal{UserID: "u1", IsAdmin: true}
	for _, c := range []struct{ method, path string }{
		{http.MethodDelete, Prefix + "/invites/a%2E%2E"},
		{http.MethodGet, Prefix + "/invites/" + strings.Repeat("x", 65) + "/devices"},
		{http.MethodDelete, Prefix + "/invites/i1/devices/p%201"},
	} {
		if rec := as(h, admin, c.method, c.path, ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s %s: %d", c.method, c.path, rec.Code)
		}
	}
	if len(*seen) != 0 {
		t.Fatalf("the agent saw %q", *seen)
	}
}

// Unlinked, the home says so (409 cloud_not_linked); an unreachable agent is a 502.
func TestUnlinkedAndUnreachable(t *testing.T) {
	t.Setenv("MAKTABA_CLOUDLINK_ADMIN", "")
	admin := &principal.Principal{UserID: "u1", IsAdmin: true}
	rec := as(&Handler{}, admin, http.MethodGet, Prefix+"/invites", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "cloud_not_linked") {
		t.Fatalf("unlinked: %d %s", rec.Code, rec.Body)
	}
	rec = as(&Handler{AgentAddr: "127.0.0.1:1"}, admin, http.MethodGet, Prefix+"/invites", "")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "cloud_unreachable") {
		t.Fatalf("unreachable: %d %s", rec.Code, rec.Body)
	}
}
