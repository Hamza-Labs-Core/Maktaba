package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	catalog "github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// T08 (story 51.09): every mounted route needs a component or is core; every mapped route
// is mounted and names a switchable catalog component.
func TestEveryRouteHasComponent(t *testing.T) {
	r := everyMount(t)
	var missing []string
	mounted := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		mounted[method+" "+route] = true
		if _, ok := ComponentOf(method, route); !ok {
			missing = append(missing, method+" "+route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d route(s) neither mapped to a component nor core (add them to router/components_routes.go):\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	for _, rule := range ComponentRoutes {
		if rule.Exact == "" || !mounted[rule.Exact] {
			t.Errorf("ComponentRoutes names %q, which is not mounted", rule.Exact)
		}
		if c, ok := catalog.Default().Get(rule.Component); !ok || !c.Switchable {
			t.Errorf("%s: %q is not a switchable component", rule.Exact, rule.Component)
		}
	}
}

// gated is a router with the component_off check over caps, every route answering 200.
func gated(caps *components.Capabilities, routes ...string) chi.Router {
	r := chi.NewRouter()
	r.Use(ComponentOff(caps))
	for _, rt := range routes {
		m, p, _ := strings.Cut(rt, " ")
		r.Method(m, p, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	}
	return r
}

func livetvOff() *components.Capabilities {
	caps := components.NewCapabilities(components.CapabilitiesOptions{})
	caps.Apply(map[string]store.Desired{"livetv": {ID: "livetv", Want: catalog.WantInstalled, Generation: 2}}, 9)
	return caps
}

// T09: with livetv off, what is on now answers 409 component_off naming livetv and live_tv.
func TestComponentOffAnswer(t *testing.T) {
	r := gated(livetvOff(), "GET /api/channels/now", "GET /api/channels/{id}/guide")
	for _, path := range []string{"/api/channels/now", "/api/channels/abc/guide"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		var b map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &b)
		if rec.Code != http.StatusConflict || b["code"] != "component_off" || b["component"] != "livetv" ||
			b["feature"] != "live_tv" || b["type"] != "https://maktaba.tv/problems/component-off" || b["status"] != float64(409) {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	// livetv on again: 200
	caps := livetvOff()
	caps.Apply(nil, 10)
	rec := httptest.NewRecorder()
	gated(caps, "GET /api/channels/now").ServeHTTP(rec, httptest.NewRequest("GET", "/api/channels/now", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("on: %d", rec.Code)
	}
}

// T10: with livetv off, its settings stay reachable: the TV rules and the channel list.
func TestComponentOffLeavesSettings(t *testing.T) {
	r := gated(livetvOff(), "PATCH /api/rules/{section}", "GET /api/channels", "PATCH /api/channels/{id}")
	for _, c := range [][2]string{{"PATCH", "/api/rules/tv"}, {"GET", "/api/channels"}, {"PATCH", "/api/channels/x"}} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(c[0], c[1], nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", c[0], c[1], rec.Code, rec.Body)
		}
	}
}

// The kill switch and a nil snapshot leave every route as it is.
func TestComponentOffKillSwitch(t *testing.T) {
	caps := components.NewCapabilities(components.CapabilitiesOptions{AllOn: true})
	caps.Apply(map[string]store.Desired{"livetv": {ID: "livetv", Want: catalog.WantInstalled}}, 9)
	for _, c := range []*components.Capabilities{caps, nil} {
		rec := httptest.NewRecorder()
		gated(c, "GET /api/channels/now").ServeHTTP(rec, httptest.NewRequest("GET", "/api/channels/now", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%d", rec.Code)
		}
	}
}
