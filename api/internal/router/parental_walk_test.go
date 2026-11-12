package router

// Epic 57 stories 57.03 and 57.13: the surface registry cannot drift from the router.
//
//   - TestEveryRouteHasParentalSurface: every route the home mounts has an entry in
//     parental.Surfaces. A new route fails until someone says what it is to a restricted
//     account (filtered, checked by id, open with a reason, refused, or closed).
//   - TestParentalSurfacesAreMounted: an entry whose route is gone fails until it is removed,
//     so the registry does not keep reasons for routes that no longer exist.
//   - TestParentalSurfacesAreSound: only the classes that refuse are given by prefix, every
//     entry has a reason, and a by-id entry names a parameter its pattern has.

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	authmw "github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/placement"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// parentalUnwalked are the registry's entries for routes apiSurface does not mount (their
// mounts need services a walk cannot start). Each is checked where it is mounted.
var parentalUnwalked = map[string]string{
	"/healthz":          "main.go mounts it on the public mux",
	"/api/panels":       "MountP54 (needs the components service)",
	"/api/panels/image": "MountP54 (needs the components service)",
	// the routes of parental.DatabaseDown that a machine's own bootstrap mounts
	"/api/.well-known/jwks.json":             "main.go mounts it on the public mux",
	"/api/cluster/emergency":                 "MountNodeLocal, on a full node (node_bootstrap.go)",
	"/api/cluster/term-claim":                "MountNodeLocal, on a full node",
	"/api/cluster/moved/{node}":              "MountNodeLocal",
	"/api/cluster/kept-aside/report":         "the home's bootstrap (Epic 37)",
	"/api/cluster/switchover":                "switchover.Orchestrator.Mount, on the home",
	"/api/cluster/switchover/standby":        "switchover.Orchestrator.Mount",
	"/api/cluster/switchover/{id}/cancel":    "switchover.Orchestrator.Mount",
	"/api/cluster/switchover/{id}/checklist": "switchover.Orchestrator.Mount",
	"/api/cluster/link":                      "the cluster link's server (cluster/link)",
	"/api/cluster/link/stream":               "the cluster link's server",
	"/api/cluster/link/pull":                 "the cluster link's server",
	"/api/cluster/link/local/agent":          "the cluster link's server, on a node",
	"/api/cluster/link/local/fetch":          "the cluster link's server, on a node",
	"/api/cluster/ca.crt":                    "the cluster link's server",
	"/internal/updates/restored":             "updates.Handler.Mount (needs the updater)",
	"/internal/plugins/invocations":          "MountP54 (needs the components service)",
	"/internal/plugins/invocations/{id}":     "MountP54",
	"/internal/plugins/breaker":              "MountP54",
	"/internal/plugins/outcome":              "MountP54",
}

// The routes that must answer when the home's database cannot be read are Open: the gate
// lets an Open route through before it reads a policy (parental.TestGateReadsNoPolicyForAnOpenRoute).
// CI's replica job once failed because GET /api/cluster/emergency, the route for exactly
// that moment, was refused 503 by the gate's policy read.
func TestDatabaseDownRoutesAreOpen(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range parental.DatabaseDown {
		if d.Reason == "" {
			t.Errorf("%s: no reason", d.Pattern)
		}
		if seen[d.Pattern] {
			t.Errorf("%s is listed twice", d.Pattern)
		}
		seen[d.Pattern] = true
		for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			if s, ok := parental.Surfaces.Lookup(m, d.Pattern); !ok || s.Class != parental.Open || s.Prefix {
				t.Errorf("%s %s must answer with the database down but is %s in parental.Surfaces (found %v): class it Open by name", m, d.Pattern, s.Class, ok)
			}
		}
	}
}

// databaseDownContent are the machine-local routes that are not in parental.DatabaseDown
// because they return content: they need the caller's limit, and with the database down
// they are served on the cache's bounded "no limit a moment ago" answer or refused.
var databaseDownContent = map[string]string{
	"/api/stream/sessions":             "opening a stream of a video: Filtered",
	"/api/stream/sessions/{id}":        "a stream session's URLs: Filtered (reading) and Guarded (closing)",
	"/api/stream/sessions/{id}/errors": "a stream's playback failure, with the session's limit: Guarded",
}

// No route that works without the home can be forgotten: every route this machine answers
// itself (router.Placement's local class) and every route that needs no sign-in
// (middleware.DefaultPublicAllowlist: each authenticates itself) is in
// parental.DatabaseDown, or is one of the content routes above.
func TestMachineLocalAndSelfAuthenticatingRoutesAreDatabaseDown(t *testing.T) {
	down := map[string]bool{}
	for _, d := range parental.DatabaseDown {
		down[d.Pattern] = true
	}
	local := 0
	for _, rule := range Placement {
		if rule.Class != placement.Local || rule.Pattern == "" {
			continue
		}
		local++
		if !down[rule.Pattern] && databaseDownContent[rule.Pattern] == "" {
			t.Errorf("%s is answered by this machine itself (router.Placement) but is not in parental.DatabaseDown: "+
				"the gate would refuse it when the home's database is down", rule.Pattern)
		}
	}
	if local < 20 {
		t.Fatalf("only %d machine-local routes found in router.Placement", local)
	}
	wild := regexp.MustCompile(`\{[^}]+\}`)
	public := authmw.DefaultPublicAllowlist()
	if len(public) < 30 {
		t.Fatalf("only %d paths need no sign-in", len(public))
	}
	for path := range public {
		found := false
		for p := range down {
			if p == path || wild.ReplaceAllString(p, "*") == path {
				found = true
			}
			// "/x/*" is one more segment under /x/: each such route is named
			if pre, ok := strings.CutSuffix(path, "*"); ok && strings.HasPrefix(p, pre) && !strings.Contains(strings.TrimPrefix(p, pre), "/") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s needs no sign-in (it authenticates itself) but is not in parental.DatabaseDown: a request that also "+
				"carries a user's sign-in would be refused there when the policies cannot be read", path)
		}
	}
	for p, why := range databaseDownContent {
		if down[p] {
			t.Errorf("%s (%s) is both a content route and in parental.DatabaseDown", p, why)
		}
	}
}

type walkedRoute struct{ method, pattern string }

func walkedRoutes(t *testing.T) []walkedRoute {
	t.Helper()
	var out []walkedRoute
	seen := map[walkedRoute]bool{}
	err := chi.Walk(apiSurface(t), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if len(route) > 1 {
			route = strings.TrimSuffix(route, "/")
		}
		wr := walkedRoute{method, route}
		if !seen[wr] {
			seen[wr] = true
			out = append(out, wr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 400 {
		t.Fatalf("only %d routes walked: a surface is missing from apiSurface", len(out))
	}
	return out
}

func TestEveryRouteHasParentalSurface(t *testing.T) {
	var missing []string
	for _, wr := range walkedRoutes(t) {
		if _, ok := parental.Surfaces.Lookup(wr.method, wr.pattern); !ok {
			missing = append(missing, wr.method+" "+wr.pattern)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d route(s) have no entry in parental.Surfaces (api/internal/parental/surfaces.go). Say what each is to a "+
			"restricted account: Filtered or ByID if it returns content and applies the filter, Open with the reason it needs "+
			"none, Refuse, or Closed until it is filtered. Until then a restricted account gets 404 from it:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

func TestParentalSurfacesAreMounted(t *testing.T) {
	routes := walkedRoutes(t)
	var stale []string
	for _, s := range parental.Surfaces {
		if _, ok := parentalUnwalked[s.Pattern]; ok {
			continue
		}
		used := false
		for _, wr := range routes {
			if s.Prefix && strings.HasPrefix(wr.pattern, s.Pattern) {
				used = true
			} else if !s.Prefix && wr.pattern == s.Pattern {
				if len(s.Methods) == 0 {
					used = true
				}
				for _, m := range s.Methods {
					if m == wr.method {
						used = true
					}
				}
			}
			if used {
				break
			}
		}
		if !used {
			stale = append(stale, strings.Join(s.Methods, ",")+" "+s.Pattern)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("parental.Surfaces names %d route(s) that are not mounted; remove each:\n  %s", len(stale), strings.Join(stale, "\n  "))
	}
}

// TestParentalSurfacesEveryMethodNamed: an exact entry that lists methods must cover each
// method it lists on a mounted route, so an entry cannot name a method the route lacks.
func TestParentalSurfacesEveryMethodNamed(t *testing.T) {
	mounted := map[walkedRoute]bool{}
	for _, wr := range walkedRoutes(t) {
		mounted[wr] = true
	}
	for _, s := range parental.Surfaces {
		if s.Prefix || len(s.Methods) == 0 {
			continue
		}
		if _, ok := parentalUnwalked[s.Pattern]; ok {
			continue
		}
		any := false
		for _, m := range s.Methods {
			if mounted[walkedRoute{m, s.Pattern}] {
				any = true
			}
		}
		if !any {
			t.Errorf("%v %s: none of its methods is mounted", s.Methods, s.Pattern)
		}
	}
}

func TestParentalSurfacesAreSound(t *testing.T) {
	kinds := map[string]bool{
		parental.SubjectVideo: true, parental.SubjectSeries: true, parental.SubjectCollection: true,
		parental.SubjectLibrary: true, parental.SubjectChannel: true,
	}
	seen := map[walkedRoute]bool{}
	patterns := map[string]bool{}
	for _, s := range parental.Surfaces {
		name := strings.Join(s.Methods, ",") + " " + s.Pattern
		if s.Reason == "" {
			t.Errorf("%s: no reason", name)
		}
		if s.Class.String() == "unknown" {
			t.Errorf("%s: no class", name)
		}
		if s.Prefix && s.Class != parental.Refuse && s.Class != parental.Closed {
			t.Errorf("%s: a prefix entry may only refuse or close (it is %s); name each route that answers a restricted account", name, s.Class)
		}
		if s.Class == parental.ByID {
			if !kinds[s.Kind] {
				t.Errorf("%s: by-id kind %q is not one Allowed checks", name, s.Kind)
			}
			if !strings.Contains(s.Pattern, "{"+s.Param+"}") {
				t.Errorf("%s: the pattern has no {%s}", name, s.Param)
			}
		}
		if s.Owner != "" && s.Class != parental.Closed {
			t.Errorf("%s: only a closed entry waits for an owner", name)
		}
		if s.Prefix {
			continue
		}
		methods := s.Methods
		if len(methods) == 0 {
			methods = []string{"*"}
		}
		for _, m := range methods {
			k := walkedRoute{m, s.Pattern}
			if seen[k] || seen[walkedRoute{"*", s.Pattern}] || (m == "*" && patterns[s.Pattern]) {
				t.Errorf("%s: the route is named twice", name)
			}
			seen[k] = true
		}
		patterns[s.Pattern] = true
	}
}
