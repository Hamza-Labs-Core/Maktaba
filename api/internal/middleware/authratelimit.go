package middleware

import (
	"net/http"
	"strconv"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Story 23.6 AC-1: the sensitive-auth surface needs per-route caps far
// tighter than the generic 6000/min/IP DoS limiter (PerIP). A
// credential-issuing endpoint hit 6000×/min/IP is a brute-force; the
// generic limiter is the wrong tool. This file is the declarative
// per-route policy table + the middleware that enforces it, keyed per
// remote IP (the caller has no authenticated user yet on these
// routes).
//
// Scope boundary: this is the rate-limit TABLE only. Failed-login /
// brute-force LOGIN lockout (the (user,ip) sliding counter) is Epic
// 10's. We cap request *frequency* per IP; we do not touch the login
// handler, the failed-attempt counter, or session state.

// AuthRouteLimit is one row of the declarative table: an exact request
// path mapped to its per-IP-per-minute ceiling.
type AuthRouteLimit struct {
	// Path is matched exactly against r.URL.Path (mirrors how
	// RequireAuthExcept's allowlist matches — no prefix surprises).
	Path string
	// PerMin is the per-IP requests/minute ceiling for this path.
	PerMin int
}

// DefaultAuthRouteLimits is the canonical Story 23.6 AC-1 table:
//
//   - /api/auth/login   — 10/min/IP  (brute-force ceiling)
//   - /api/auth/refresh — 60/min/IP  (a busy client rotates often)
//   - other auth routes — 30/min/IP  (logout / logout-all)
//
// Declarative and in-code (no DB, no migration): the table is small,
// static, and security-critical — config drift here is a vulnerability,
// so it ships compiled-in. Operators tune the generic limiter via env;
// these hard auth caps are intentionally not env-softened.
func DefaultAuthRouteLimits() []AuthRouteLimit {
	return []AuthRouteLimit{
		{Path: "/api/auth/login", PerMin: 10},
		{Path: "/api/auth/refresh", PerMin: 60},
		{Path: "/api/auth/logout", PerMin: 30},
		{Path: "/api/auth/logout-all", PerMin: 30},
	}
}

// AuthRouteRateLimit returns a middleware that enforces the per-route
// table. A path not in the table passes straight through (the generic
// PerIP limiter still bounds it). Matched paths get their own per-IP
// token bucket; exhaustion yields the same structured 429 +
// Retry-After envelope the generic limiter uses, so clients have one
// shape to handle.
//
// Wired as a distinct, self-contained layer in the security chain so a
// union merge with Epic 10's middleware edits stays clean — it does
// not modify PerIP/PerUser or the auth middlewares.
func AuthRouteRateLimit(table []AuthRouteLimit) func(http.Handler) http.Handler {
	stores := make(map[string]*rlStore, len(table))
	for _, row := range table {
		if row.Path == "" || row.PerMin <= 0 {
			continue
		}
		s := newRLStore(row.PerMin)
		go runSweep(s)
		stores[row.Path] = s
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s, ok := stores[r.URL.Path]
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			ip := clientIP(r)
			if allow, retry := s.take("authroute:" + r.URL.Path + ":" + ip); !allow {
				w.Header().Set("Retry-After", strconv.Itoa(retry))
				httperror.Write(w, r, &httperror.Error{
					Type:   httperror.TypeRateLimited,
					Title:  "too many requests",
					Status: http.StatusTooManyRequests,
					Detail: "per-route auth rate limit exceeded",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
