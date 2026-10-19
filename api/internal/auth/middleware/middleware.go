// Package middleware wires authentication into the HTTP request
// pipeline.
//
// Two ordered checks run in front of every protected handler:
//
//  1. AdminToken: if `MAKTABA_ADMIN_TOKEN` is set and the request
//     carries it as a bearer or cookie, the principal is the sentinel
//     admin user (Story 10.9). The DB is not touched.
//
//  2. JWTBearer: if an `Authorization: Bearer <jwt>` header is
//     present, the JWT is verified offline against the trust set
//     (Story 10.3 / 10.6) and the principal is constructed from the
//     `usr`, `lib`, and `is_admin` claims.
//
// Each middleware ATTACHES a principal to the context if it succeeds
// and leaves the request alone if no credential is present. A
// downstream `RequireAuth` handler turns "no principal" into 401.
// This split lets anonymous endpoints (health, JWKS) sit on the same
// router without per-route wiring.
package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secret"
)

// MinAdminTokenLen is Story 10.9 EC-2: refuse `MAKTABA_ADMIN_TOKEN`
// values shorter than 32 chars.
const MinAdminTokenLen = 32

// AdminCookieName is the cookie name the SPA stores the admin token
// in once the user has pasted it via the bootstrap dialog
// (Story 10.9 AC-3).
const AdminCookieName = "mkt_admin_token"

// AdminToken returns a middleware that enables the single-user
// admin-token bypass path (Story 10.9).
//
// `tok` is the configured admin token; pass an empty Value to disable
// the bypass entirely. The middleware uses constant-time comparison
// (AC-2) so a per-byte timing attack can't probe a candidate.
func AdminToken(tok secret.Value) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !tok.Present() {
				next.ServeHTTP(w, r)
				return
			}
			candidate := readBearer(r)
			if candidate == "" {
				if c, err := r.Cookie(AdminCookieName); err == nil {
					candidate = c.Value
				}
			}
			if candidate == "" {
				next.ServeHTTP(w, r)
				return
			}
			want := tok.Reveal()
			// constant-time across both length and content: pad both
			// to the same length first via subtle.ConstantTimeCompare.
			if subtle.ConstantTimeCompare([]byte(candidate), []byte(want)) != 1 {
				next.ServeHTTP(w, r)
				return
			}
			p := &principal.Principal{
				UserID:             users.SentinelAdminID,
				IsAdmin:            true,
				AccessAllLibraries: true,
				Source:             principal.SourceAdminToken,
			}
			next.ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(), p)))
		})
	}
}

// JWTBearer returns a middleware that, for any `Authorization: Bearer
// <jwt>` request, verifies the JWT against `set` and attaches the
// resulting principal to the context.
//
// `expectedAud` is the audience the API expects on its own tokens
// (typically "api"). A token with the wrong audience is rejected
// silently — the request continues anonymously and a downstream
// RequireAuth turns it into 401. This split avoids leaking which step
// of validation failed.
func JWTBearer(set *keys.Set, expectedAud string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If a previous middleware (admin-token) already set a
			// principal, leave it alone. Both paths logically agree on
			// who the caller is; we just don't waste cycles re-verifying.
			if principal.FromContext(r.Context()) != nil {
				next.ServeHTTP(w, r)
				return
			}
			tok := readBearer(r)
			if tok == "" {
				next.ServeHTTP(w, r)
				return
			}
			c, err := jwt.Verify(set, tok, expectedAud)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			p := &principal.Principal{
				UserID:             c.Usr,
				IsAdmin:            c.IsAdmin,
				Libraries:          c.Lib,
				AccessAllLibraries: c.IsAdmin,
				Source:             principal.SourceJWT,
			}
			next.ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(), p)))
		})
	}
}

// RequireAuth is the gate for protected routes: 401 unless an upstream
// middleware put a principal on the context.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal.FromContext(r.Context()) == nil {
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// DefaultPublicAllowlist is the exact set of paths that stay reachable
// without authentication. Everything else served by the public mux is
// gated by RequireAuthExcept.
//
// Justification per route:
//
//   - /healthz, /api/system/health, /api/system/version — liveness /
//     readiness / build probes the orchestrator polls with no creds.
//   - /api/.well-known/jwks.json — JWKS publication; clients and the
//     streaming service fetch it to *verify* tokens, so gating it would
//     be circular.
//   - /.well-known/security.txt — RFC 9116 mandates unauthenticated
//     reachability.
//   - /api/auth/login — issues the first credential; the caller has
//     none yet by definition.
//   - /api/auth/refresh — rotates a refresh token; the caller's access
//     token may already be expired, so it cannot be required here.
//   - /api/cluster/join and /api/cluster/join/{node_id} (Epic 31) — a
//     machine on the LAN asks to join and polls for the answer before it
//     holds any credential. The handler answers joins only while the
//     admin has switched them on, rate-limits per IP, and the poll needs
//     the machine's own public key.
//   - /api/cluster/agent (Epic 31 phase 2) — the cluster channel of every
//     process: it proves possession of its pairing key (or the home agent
//     key) inside the WebSocket handshake, and attempts are rate-limited
//     per IP.
//   - /api/tv/remote/webhook (Epic 32.7) — Cloudflare Notifications cannot
//     sign in; the handler checks the cf-webhook-auth secret and only
//     asks the remote controller for a reconcile.
//
// Deliberately NOT public: /api/system/sbom (internal artifact;
// only security.txt is mandated public), /api/auth/logout[-all]
// (operate on an authenticated identity), and every /api/stream/*
// route (each already enforces a principal in-handler). The api
// service serves no signed-URL playback endpoint — those live in the
// separate streaming service with its own auth.
func DefaultPublicAllowlist() map[string]struct{} {
	return map[string]struct{}{
		"/healthz":                   {},
		"/api/system/health":         {},
		"/api/system/version":        {},
		"/api/.well-known/jwks.json": {},
		"/.well-known/security.txt":  {},
		"/api/auth/login":            {},
		"/api/auth/refresh":          {},
		// web-pages-batch2 — self-service entry points the caller reaches
		// before holding any credential.
		"/api/auth/register":        {},
		"/api/auth/forgot-password": {},
		"/api/auth/reset-password":  {},
		// Epic 31 — a machine's join request and its poll.
		"/api/cluster/join":   {},
		"/api/cluster/join/*": {},
		// Epic 31 phase 2 — every process's channel to the home; it
		// authenticates in the protocol (pairing key or home agent key).
		"/api/cluster/agent": {},
		// Epic 32 story 32.7 — Cloudflare Notifications for the live inputs;
		// the handler refuses any request without the webhook secret header.
		"/api/tv/remote/webhook": {},
		// Epic 35 story 35.11 — every machine's identity (never a library or
		// user name): the apps and a typed address recognise the cluster.
		"/api/cluster/self": {},
		// Epic 35 story 35.9 — the cluster sign-in: authorize sends a browser
		// that is not signed in to the home's sign-in page (and checks the
		// session itself); start and callback run before the node holds a
		// session. Introspect and exchange are a node's calls: the handlers
		// refuse any request without a verified node signature.
		"/api/cluster/sso/authorize":       {},
		"/api/cluster/sso/start":           {},
		"/api/cluster/sso/callback":        {},
		"/api/cluster/sso/exchange":        {},
		"/api/cluster/delegate/introspect": {},
	}
}

// RequireAuthExcept is the global gate for the public mux: it behaves
// like RequireAuth (401 unless an upstream middleware attached a
// principal) for every request whose exact path is NOT in `public`.
// Allowlisted paths pass straight through anonymously.
//
// Exact-match only (not prefix): `/api/auth/login` being public must
// not make `/api/auth/login-as-admin` public too. The one wildcard form
// is a key ending in "/*", which matches exactly one more non-empty path
// segment (`/api/cluster/join/*` matches `/api/cluster/join/<id>`, not
// `/api/cluster/join/<id>/x`).
func RequireAuthExcept(public map[string]struct{}) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublic(public, r.URL.Path) || IsPublicChannelArt(r) {
				next.ServeHTTP(w, r)
				return
			}
			if principal.FromContext(r.Context()) == nil {
				writeProblem(w, http.StatusUnauthorized, "unauthorized", "authentication required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isPublic(public map[string]struct{}, path string) bool {
	if _, ok := public[path]; ok {
		return true
	}
	i := strings.LastIndexByte(path, '/')
	if i < 0 || i == len(path)-1 {
		return false
	}
	_, ok := public[path[:i+1]+"*"]
	return ok
}

// RequireAdmin gates routes that must be admin-only. Returns 403 for
// non-admin authenticated users; falls through to RequireAuth for the
// not-authenticated case.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principal.FromContext(r.Context())
		if p == nil {
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if !p.IsAdmin {
			writeProblem(w, http.StatusForbidden, "forbidden", "")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// readBearer extracts the token from `Authorization: Bearer <tok>`.
// Returns the empty string when the header is absent or malformed.
func readBearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if v == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(v, prefix) {
		return ""
	}
	return strings.TrimSpace(v[len(prefix):])
}

// IsPublicChannelArt reports whether r reads a channel's art image:
// GET/HEAD /api/channels/{id}/logo or /api/channels/{id}/art/{logo|
// watermark|loading}. Those stay anonymous: a channel picture is not
// sensitive, and the players that load it (Plex reading the XMLTV icon,
// an <img> on another origin) carry no session. Only that exact shape is
// public (one id segment, a known kind), never any other channel route.
func IsPublicChannelArt(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/channels/")
	if !ok {
		return false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" {
		return false
	}
	switch {
	case len(parts) == 2:
		return parts[1] == "logo"
	case len(parts) == 3 && parts[1] == "art":
		return parts[2] == "logo" || parts[2] == "watermark" || parts[2] == "loading"
	}
	return false
}
