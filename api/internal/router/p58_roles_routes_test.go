package router

// Epic 58 story 58.17 T02: the router walk, extended to the cast principal and to Epic 58's
// routes.
//
//   - TestCastGrantWalkRefusesEveryOtherRoute: every route the home mounts is called as a
//     cast grant through the grant's gate. A route the gate's list (castgrant.Routes) does not
//     name answers 403 `cast_scope` and never reaches its handler; a route added anywhere is
//     in the walk, so a grant can never reach it by default.
//   - P58Routes: Epic 58's routes as "METHOD pattern", from the same walk, for the role table
//     (p58_roles_pg_test.go, package router_test) to be held to: every route a TV client or a
//     cast grant calls that this epic added or changed.

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/castgrant"
)

// p58Named are Epic 58's routes (each story's own, and the ones whose answer it changed).
var p58Named = map[string]bool{
	"GET /api/discovery/home":                 true, // api_level, api_level_oldest, client_floor (58.01)
	"POST /api/stream/sessions":               true, // device_caps (58.02)
	"POST /api/stream/sessions/{id}/progress": true, // a cast receiver's progress (58.07)
	"POST /api/pairing/cloud-approve":         true, // the cloud code (58.03)
	"POST /api/pairing/cloud-assertion":       true, // the cloud code, approved on maktaba.tv (58.03)
	"GET /api/pairing/cloud-code/{code}":      true, // the approval screen's lookup (58.03)
	"GET /api/me/app-sign-ins":                true, // the cloud-code TV in the approver's list (58.03)
	"DELETE /api/me/app-sign-ins/{id}":        true,
	"POST /api/cast/grants":                   true, // the cast grant (58.07)
}

// IsP58Route reports whether "METHOD pattern" is one of Epic 58's: named above, or a route a
// cast grant may call.
func IsP58Route(key string) bool {
	_, cast := castgrant.Routes[key]
	return p58Named[key] || cast
}

// P58Routes lists Epic 58's mounted routes as "METHOD pattern", sorted.
func P58Routes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, wr := range walkedRoutes(t) {
		if k := wr.method + " " + wr.pattern; IsP58Route(k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Every route p58Named names is mounted: a renamed route leaves its row unchecked otherwise.
func TestP58NamedRoutesAreMounted(t *testing.T) {
	mounted := map[string]bool{}
	for _, k := range P58Routes(t) {
		mounted[k] = true
	}
	for k := range p58Named {
		if !mounted[k] {
			t.Errorf("p58Named names %s, which the home does not mount", k)
		}
	}
	for k := range castgrant.Routes {
		if !mounted[k] {
			t.Errorf("castgrant.Routes names %s, which the home does not mount", k)
		}
	}
}

var walkParam = regexp.MustCompile(`\{[^}]+\}`)

// walkPath fills a pattern's parameters, and its wildcard, so the router finds the pattern.
func walkPath(pattern string) string {
	p := walkParam.ReplaceAllStringFunc(pattern, func(s string) string {
		if strings.Contains(s, ":") { // a regexp parameter: chi's own patterns here are all digits
			return "1"
		}
		return "00000000-0000-4000-8000-000000581700"
	})
	return strings.ReplaceAll(p, "*", "x")
}

func TestCastGrantWalkRefusesEveryOtherRoute(t *testing.T) {
	r := apiSurface(t)
	gate := castgrant.NewGate()
	var reached string
	next := http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) { reached = req.Method + " " + req.URL.Path })
	front := gate.Middleware(r)(next)
	grant := &principal.Principal{UserID: "00000000-0000-4000-8000-000000581701", Source: principal.SourceCast,
		Cast: &principal.CastScope{VideoID: "00000000-0000-4000-8000-000000581702"}}

	refused, allowed := 0, 0
	for _, wr := range walkedRoutes(t) {
		switch wr.method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead:
		default:
			continue
		}
		key := wr.method + " " + wr.pattern
		if _, ok := castgrant.Routes[key]; ok {
			allowed++
			continue
		}
		path := walkPath(wr.pattern)
		if found := r.Find(chi.NewRouteContext(), wr.method, path); strings.TrimSuffix(found, "/") != wr.pattern {
			// another pattern answers this path first (a fixed segment beside a parameter):
			// that pattern is walked on its own
			if _, ok := castgrant.Routes[wr.method+" "+strings.TrimSuffix(found, "/")]; ok {
				t.Errorf("%s: the filled path %s is found as %s, a route a grant may call", key, path, found)
			}
			continue
		}
		reached = ""
		req := httptest.NewRequest(wr.method, path, nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), grant))
		rec := httptest.NewRecorder()
		front.ServeHTTP(rec, req)
		if reached != "" || rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"cast_scope"`) {
			t.Errorf("%s as a cast grant = %d %s (reached the handler: %v), want 403 cast_scope", key, rec.Code,
				strings.TrimSpace(rec.Body.String()), reached != "")
		}
		refused++
	}
	if allowed != len(castgrant.Routes) {
		t.Errorf("the walk found %d of the %d routes a grant may call", allowed, len(castgrant.Routes))
	}
	if refused < 400 {
		t.Errorf("only %d routes were refused to the grant: the walk is missing a surface", refused)
	}
	t.Logf("a cast grant: %d routes refused, %d allowed (castgrant.Routes)", refused, allowed)
}
