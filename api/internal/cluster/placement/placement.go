// Package placement says where each API route is served when the API runs on a full
// node (Epic 35 story 35.10, design §11.1):
//
//	local  by this machine (its version, processes, health, its own VOD sessions, the
//	       cluster sign-in's node half)
//	any    by whichever API gets it, from the database through the node's role (library
//	       browse, video detail, collections, queue and job reads, guide reads, settings
//	       reads) — only while the schema guard says the node's code fits
//	home   proxied to the home's API with the user's session (auth, users, watch data,
//	       search, home caches, every write, admin, TV, cluster, updates, sockets)
//
// On the home every class is served locally. A route that no rule names is `home`
// (a missing class can only make a route safer); a test walks every mounted route and
// fails on one no rule names (router.TestEveryRouteHasPlacementClass).
package placement

import (
	"net/http"
	"strings"
)

// Class is a route's placement.
type Class string

// The classes.
const (
	Local Class = "local"
	Any   Class = "any"
	Home  Class = "home"
)

// Rule classes routes: one chi pattern (exact) or every pattern under a prefix
// (Prefix ends with "/" or names a whole pattern family, e.g. "/api/tv/"). Methods
// empty: every method.
type Rule struct {
	Methods []string
	Pattern string
	Prefix  string
	Class   Class
}

// Matches reports whether the rule names method + pattern.
func (r Rule) Matches(method, pattern string) bool {
	if len(r.Methods) > 0 {
		ok := false
		for _, m := range r.Methods {
			if strings.EqualFold(m, method) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if r.Pattern != "" {
		return r.Pattern == pattern
	}
	return r.Prefix != "" && strings.HasPrefix(pattern, r.Prefix)
}

// Table is an ordered rule list: exact patterns win over prefixes, then the first
// matching rule.
type Table []Rule

// Classify returns the class of a route pattern and whether a rule named it.
func (t Table) Classify(method, pattern string) (Class, bool) {
	for _, r := range t {
		if r.Pattern != "" && r.Matches(method, pattern) {
			return r.Class, true
		}
	}
	for _, r := range t {
		if r.Pattern == "" && r.Matches(method, pattern) {
			return r.Class, true
		}
	}
	return Home, false
}

// Get, Read, All are method lists for rules.
var (
	Get  = []string{http.MethodGet, http.MethodHead}
	Read = []string{http.MethodGet, http.MethodHead, http.MethodOptions}
)

// Exact builds exact-pattern rules of one class.
func Exact(class Class, methods []string, patterns ...string) []Rule {
	out := make([]Rule, 0, len(patterns))
	for _, p := range patterns {
		out = append(out, Rule{Methods: methods, Pattern: p, Class: class})
	}
	return out
}

// Prefixes builds prefix rules of one class (every method).
func Prefixes(class Class, prefixes ...string) []Rule {
	out := make([]Rule, 0, len(prefixes))
	for _, p := range prefixes {
		out = append(out, Rule{Prefix: p, Class: class})
	}
	return out
}
