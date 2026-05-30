package auth

import (
	"crypto/subtle"
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// CSRFHeader is the request header the SPA echoes the `mkt_csrf` cookie
// value back in on every state-changing request (Story 10.10 AC-2).
const CSRFHeader = "X-Maktaba-CSRF"

// CSRF returns a middleware that enforces the double-submit CSRF
// pattern for cookie-authenticated, state-changing requests
// (Story 10.10).
//
// The guard is deliberately narrow so it cannot break the bearer/API
// path or the public allowlist:
//
//   - Safe methods (GET/HEAD/OPTIONS) are exempt (AC-4) — they don't
//     mutate state, so a forged cross-site GET is harmless here.
//   - A request with NO principal passes straight through. The
//     downstream RequireAuthExcept gate owns the 401 for protected
//     paths; the public allowlist (login/refresh) must stay reachable
//     so a credential can be issued in the first place. CSRF only
//     protects an *already authenticated* ambient-cookie session.
//   - A request whose principal was NOT established by a session
//     cookie (SourceJWT / SourceAdminToken) is exempt (AC-3). Bearer
//     and admin-token clients send an explicit Authorization header /
//     token, not an ambient cookie, so they are not CSRF-able. Belt
//     and braces: a request carrying an Authorization: Bearer header
//     is also treated as an API client and skipped, so an API client
//     that happens to also carry a stale session cookie is not broken.
//   - Otherwise (cookie-sourced principal, unsafe method) the
//     `mkt_csrf` cookie value must be present and constant-time-equal
//     to the X-Maktaba-CSRF header, else 403 `csrf-mismatch`.
//
// Install AFTER the credential-attaching middlewares (so Source is
// populated) and BEFORE the handlers, alongside RequireAuthExcept.
func (h *Handler) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		p := principal.FromContext(r.Context())
		if p == nil {
			// Anonymous: the auth gate decides (401 for protected,
			// pass-through for the public login/refresh allowlist).
			next.ServeHTTP(w, r)
			return
		}
		// Only ambient session-cookie auth is CSRF-able. Bearer/admin
		// token requests carry an explicit credential header.
		if p.Source != principal.SourceCookie || r.Header.Get("Authorization") != "" {
			next.ServeHTTP(w, r)
			return
		}

		c, err := r.Cookie(CookieCSRF)
		header := r.Header.Get(CSRFHeader)
		if err != nil || c.Value == "" || header == "" ||
			subtle.ConstantTimeCompare([]byte(c.Value), []byte(header)) != 1 {
			httperror.Write(w, r, &httperror.Error{
				Type:   TypeCSRFMismatch,
				Title:  "csrf token mismatch",
				Status: http.StatusForbidden,
				Detail: "missing or invalid X-Maktaba-CSRF header for a cookie-authenticated request",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
