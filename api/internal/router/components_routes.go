package router

// Which component each route needs (Epic 51 story 51.09). A route whose component is off
// answers 409 component_off. Only routes that need the running part are mapped: the
// guide and what is on now, tuning a channel, a YouTube search. Settings, setup and
// configuration routes stay core, so an admin can prepare a feature before turning it
// on: the Plex sign-in is setup (story 51.07 §5), so it works whatever ext.plex's state.
// A role refusal comes before component_off: a caller the route refuses anyway gets its
// 401 or 403, never a 409 naming a component. TestEveryRouteHasComponent fails when a
// newly mounted route is in neither list.

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

// componentRule maps routes to a component: Exact is "METHOD /pattern", Prefix every
// pattern under it (any method). Component "" is core: the route never answers
// component_off. Admin marks a route only an admin may call: anyone else gets 403 before
// the component is looked at (TestComponentOffRoleFirst checks it against the handlers).
type componentRule struct {
	Exact     string
	Prefix    string
	Component string
	Admin     bool
}

type componentTable []componentRule

// ComponentRoutes are the routes that need a switchable component running.
var ComponentRoutes = componentTable{
	// Live TV: the guide, what is on now, the players' playlist and guide, tuning
	{Exact: "GET /api/channels/now", Component: "livetv"},
	{Exact: "GET /api/channels/guide", Component: "livetv"},
	{Exact: "GET /api/channels/{id}/guide", Component: "livetv"},
	{Exact: "GET /api/channels/playlist.m3u", Component: "livetv"},
	{Exact: "GET /api/channels/xmltv", Component: "livetv"},
	{Exact: "GET /api/tv/onair", Component: "livetv", Admin: true},
	{Exact: "POST /api/tv/remote/tune", Component: "livetv"},
	// YouTube: the search's "From YouTube" (yt-dlp)
	{Exact: "GET /api/search/youtube", Component: "youtube"},
}

// CoreRoutes never answer component_off: every other route family. A route family
// added later goes in one of the two lists.
var CoreRoutes = componentTable{
	{Prefix: "/healthz"}, {Prefix: "/.well-known/"}, {Prefix: "/admin/"}, {Prefix: "/internal/"},
	{Prefix: "/graphql"}, {Prefix: "/ws/"},
	{Prefix: "/api/system/"}, {Prefix: "/api/.well-known/"}, {Prefix: "/api/auth/"}, {Prefix: "/api/me"},
	{Prefix: "/api/users"}, {Prefix: "/api/security/"}, {Prefix: "/api/admin/"}, {Prefix: "/api/diagnostics/"},
	{Prefix: "/api/cluster"}, {Prefix: "/api/server/"}, {Prefix: "/api/components"}, {Prefix: "/api/capabilities"},
	{Prefix: "/api/pairing/"}, {Prefix: "/api/discovery/"}, {Prefix: "/api/entitlements"}, {Prefix: "/api/devices"},
	{Prefix: "/api/notifications"}, {Prefix: "/api/activity"}, {Prefix: "/api/watch/"}, {Prefix: "/api/issues"},
	{Prefix: "/api/explain"}, {Prefix: "/api/home"}, {Prefix: "/api/workers"},
	// the library
	{Prefix: "/api/videos"}, {Prefix: "/api/libraries"}, {Prefix: "/api/collections"}, {Prefix: "/api/items/"},
	{Prefix: "/api/series"}, {Prefix: "/api/seasons/"}, {Prefix: "/api/tags"}, {Prefix: "/api/speakers"},
	{Prefix: "/api/thumbnails/"}, {Prefix: "/api/enrichment/"}, {Prefix: "/api/recommendations"},
	{Prefix: "/api/stream/"},
	// processing: the queues show what is parked; settings and models stay reachable
	{Prefix: "/api/queues"}, {Prefix: "/api/queue/"}, {Prefix: "/api/jobs"}, {Prefix: "/api/pools"},
	{Prefix: "/api/transcription/"}, {Prefix: "/api/reports"}, {Prefix: "/api/models"},
	{Prefix: "/api/settings"}, {Prefix: "/api/rules"},
	// search answers keyword results when semantic search is off (no 409)
	{Prefix: "/api/search"},
	// Live TV's and the extensions' configuration (channels, rules, Plex's settings and
	// its sign-in, the extensions' connection tests).
	// /api/tv/changes, rollback, slots and releases answer component_off themselves,
	// after their admin check (middleware.RequireComponent, story 51.05).
	{Prefix: "/api/channels"}, {Prefix: "/api/filler/"}, {Prefix: "/api/tv/"}, {Prefix: "/api/extensions"},
	// Epic 52: word review never answers component_off: fixes are the API's own edits and
	// work while review.words is off; the pages read its phase from /api/components
	{Prefix: "/api/review/"}, {Prefix: "/api/vocabulary"}, {Prefix: "/api/language-packs"},
}

func (r componentRule) matches(method, pattern string) bool {
	if r.Exact != "" {
		return r.Exact == method+" "+pattern
	}
	return r.Prefix != "" && strings.HasPrefix(pattern, r.Prefix)
}

// ComponentOf is the component a route needs ("" for core) and whether either list
// names it. Exact rules win over prefixes.
func ComponentOf(method, pattern string) (string, bool) {
	r, ok := ruleOf(method, pattern)
	return r.Component, ok
}

// ruleOf is the rule that names a route (exact rules first) and whether there is one.
func ruleOf(method, pattern string) (componentRule, bool) {
	for _, t := range []componentTable{ComponentRoutes, CoreRoutes} {
		for _, r := range t {
			if r.Exact != "" && r.matches(method, pattern) {
				return r, true
			}
		}
	}
	for _, t := range []componentTable{ComponentRoutes, CoreRoutes} {
		for _, r := range t {
			if r.Exact == "" && r.matches(method, pattern) {
				return r, true
			}
		}
	}
	return componentRule{}, false
}

// ComponentOff answers 409 component_off (middleware.ComponentOff, 51.05's problem) on a
// route whose component is off, from the capabilities snapshot (no query). With every component on (the common case) it looks
// nothing up. nil caps: a no-op.
func ComponentOff(caps *components.Capabilities) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if caps == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s := caps.Snapshot()
			if !s.AnyOff() {
				next.ServeHTTP(w, r)
				return
			}
			rctx := chi.RouteContext(r.Context())
			if rctx == nil || rctx.Routes == nil {
				next.ServeHTTP(w, r)
				return
			}
			method := r.Method
			if method == http.MethodHead {
				method = http.MethodGet
			}
			pattern := rctx.Routes.Find(chi.NewRouteContext(), method, r.URL.Path)
			rule, _ := ruleOf(method, pattern)
			comp := rule.Component
			if comp == "" {
				next.ServeHTTP(w, r)
				return
			}
			feature, off := s.ComponentOff(comp)
			if !off {
				next.ServeHTTP(w, r)
				return
			}
			// the role first: a caller the route refuses gets that answer, not a 409
			// that tells it which part of the home is off. No one signed in reaches
			// here only past the auth gate's allowlist (no gated route is on it): the
			// handler refuses it as it always does.
			p := principal.FromContext(r.Context())
			if p == nil {
				next.ServeHTTP(w, r)
				return
			}
			if rule.Admin && !p.IsAdmin {
				httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
				return
			}
			e := mw.ComponentOff(comp)
			if feature != "" {
				e = e.With("feature", feature)
			}
			httperror.Write(w, r, e)
		})
	}
}
