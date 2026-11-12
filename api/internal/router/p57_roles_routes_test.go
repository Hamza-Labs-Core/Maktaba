package router

// Epic 57 story 57.13: which of the home's routes are Epic 57's, for the role table
// (p57_roles_pg_test.go, package router_test) to be held to. The list is read from the
// walk of every mount, so a parental route added anywhere is in it.

import (
	"sort"
	"strings"
	"testing"
)

// p57Named are Epic 57's routes whose path does not say so.
var p57Named = map[string]bool{
	"/api/capabilities":                 true, // its `parental` block (57.12)
	"/api/channels/{id}/access":         true, // 57.10
	"/api/channels/{channel}/access":    true,
	"/api/channels/{id}/play":           true,
	"/api/tv/hdhr/allowed":              true,
	"/api/pairing/approve":              true, // `as_user` (57.02)
	"/api/users/{id}/parental":          true,
	"/api/me/parental-pin":              true,
	"/api/parental/library-suggestions": true,
}

// IsP57Route reports whether a route is one of Epic 57's.
func IsP57Route(pattern string) bool {
	return p57Named[pattern] || strings.Contains(pattern, "/parental") ||
		strings.HasSuffix(pattern, "/age-tier") || pattern == "/api/me/age-signal"
}

// P57Routes lists Epic 57's routes as "METHOD pattern", sorted, from the walk of every
// mount of the home.
func P57Routes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, wr := range walkedRoutes(t) {
		switch wr.method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			continue
		}
		if IsP57Route(wr.pattern) {
			out = append(out, wr.method+" "+wr.pattern)
		}
	}
	sort.Strings(out)
	return out
}
