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
