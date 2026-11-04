package router

import (
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
	catalog "github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

func as(r *http.Request, p *principal.Principal) *http.Request {
	if p == nil {
		return r
	}
	return r.WithContext(principal.WithPrincipal(r.Context(), p))
}

func viewerP() *principal.Principal {
	return &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000c1"}
}

func adminP() *principal.Principal {
	return &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000a1", IsAdmin: true, AccessAllLibraries: true}
}

// off is a snapshot with each of ids off.
func off(ids ...string) *components.Capabilities {
	caps := components.NewCapabilities(components.CapabilitiesOptions{})
	rows := map[string]store.Desired{}
	for _, id := range ids {
		rows[id] = store.Desired{ID: id, Want: catalog.WantInstalled, Generation: 2}
	}
	caps.Apply(rows, 9)
	return caps
}

var routeParam = regexp.MustCompile(`\{[^}]+\}`)

// The role first (story 51.09): with its component off, a gated route answers a caller
// it refuses anyway with that refusal (401 or 403, as with the component on), never a
// 409 naming the component; a caller it accepts gets the 409. Run against the real
// handlers, so a route only admins may call that is not marked Admin fails here.
func TestComponentOffRoleFirst(t *testing.T) {
	var ids []string
	for _, rule := range ComponentRoutes {
		ids = append(ids, rule.Component)
	}
	on, gated := everyMountWith(t, nil), everyMountWith(t, off(ids...))
	call := func(r http.Handler, method, path string, p *principal.Principal) int {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, as(httptest.NewRequest(method, path, strings.NewReader("{}")), p))
		return rec.Code
	}
	for _, rule := range ComponentRoutes {
		method, pattern, _ := strings.Cut(rule.Exact, " ")
		path := routeParam.ReplaceAllString(pattern, "x1")
		for _, who := range []struct {
			name string
			p    *principal.Principal
		}{{"no one", nil}, {"viewer", viewerP()}, {"admin", adminP()}} {
			want := call(on, method, path, who.p)
			if want != http.StatusUnauthorized && want != http.StatusForbidden {
				want = http.StatusConflict
			}
			if got := call(gated, method, path, who.p); got != want {
				t.Errorf("%s as %s with %s off: %d, want %d", rule.Exact, who.name, rule.Component, got, want)
			}
		}
	}
}

// A viewer asking for an admin-only route whose component is off gets 403, not a 409
// that names the component.
func TestComponentOffViewerGetsForbidden(t *testing.T) {
	r := gated(livetvOff(), "GET /api/tv/onair")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, as(httptest.NewRequest("GET", "/api/tv/onair", nil), viewerP()))
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "livetv") {
		t.Fatalf("viewer: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, as(httptest.NewRequest("GET", "/api/tv/onair", nil), adminP()))
	if rec.Code != http.StatusConflict {
		t.Fatalf("admin: %d %s", rec.Code, rec.Body)
	}
}

// Story 51.07 §5: the Plex sign-in is setup, so an admin signs in to Plex while ext.plex
// is off (to set it up before turning it on), and a viewer gets 403, not a 409 naming
// ext.plex.
func TestPlexSigninWhileOff(t *testing.T) {
	plextv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v2/pins" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":42,"code":"ABCD"}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(plextv.Close)
	db := queuestest.New(&queuestest.Rule{Match: "from extensions", Cols: []string{"name", "enabled", "settings", "updated_at"},
		Rows: [][]driver.Value{{"plex", false, `{"url":"http://plex:32400"}`, nil}}})
	r := chi.NewRouter()
	r.Use(ComponentOff(off("ext.plex")))
	(&queues.Handler{DB: db.DB, PlexTVURL: plextv.URL, PlexHTTP: plextv.Client()}).Mount(r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, as(httptest.NewRequest("POST", "/api/extensions/plex/signin", nil), adminP()))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"code":"ABCD"`) {
		t.Fatalf("admin with Plex off: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, as(httptest.NewRequest("POST", "/api/extensions/plex/signin", nil), viewerP()))
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "ext.plex") {
		t.Fatalf("viewer with Plex off: %d %s", rec.Code, rec.Body)
	}
	for _, rt := range []string{"POST /api/extensions/plex/signin", "GET /api/extensions/plex/signin/{id}",
		"POST /api/extensions/plex/signin/{id}/use"} {
		m, p, _ := strings.Cut(rt, " ")
		if c, ok := ComponentOf(m, p); !ok || c != "" {
			t.Errorf("%s needs %q: the sign-in is setup, core", rt, c)
		}
	}
}
