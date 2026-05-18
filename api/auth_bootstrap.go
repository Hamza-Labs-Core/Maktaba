package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httpsec"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secret"
)

// authState holds the auth surfaces the serve loop needs to wire
// into the public mux. Built once at boot from env vars per the
// Story 10.6 / 10.9 / 10.15 ACs.
type authState struct {
	keys       *keys.Set
	adminToken secret.Value
	cors       httpsec.CORSConfig
	headers    httpsec.HeadersConfig
}

// initAuth loads the auth bootstrap from the environment. Returns an
// error when the env is internally inconsistent (e.g. only one of the
// PRIVATE/PUBLIC PEMs is set), but tolerates "fully unset" — the
// caller decides whether that's fatal (production) or fine (a
// stub-stage `serve` run with no auth wired yet).
func initAuth(logger *slog.Logger) (*authState, error) {
	st := &authState{}

	// --- RS256 keys (Story 10.6 AC-1) ---
	priv := os.Getenv("MAKTABA_JWT_PRIVATE_KEY_PEM")
	pub := os.Getenv("MAKTABA_JWT_PUBLIC_KEY_PEM")
	if (priv == "") != (pub == "") {
		return nil, errors.New("auth: MAKTABA_JWT_PRIVATE_KEY_PEM and MAKTABA_JWT_PUBLIC_KEY_PEM must be set together")
	}
	overlap := DefaultRotationOverlap()
	st.keys = keys.NewSet(overlap)
	if priv != "" {
		k, err := keys.FromPEM(priv, pub)
		if err != nil {
			return nil, fmt.Errorf("auth: load JWT keys: %w", err)
		}
		st.keys.Replace(k)
		logger.Info("auth: loaded JWT signing key", "kid", k.KID, "event", "auth_keys_loaded")
	} else {
		logger.Warn("auth: no JWT keys configured; JWT verification will fail until MAKTABA_JWT_*_PEM is set",
			"event", "auth_keys_missing")
	}

	// --- Single-user admin token (Story 10.9 AC-1, EC-2) ---
	tokVal := secret.FromEnvOrFile("MAKTABA_ADMIN_TOKEN", "", logger)
	if tokVal.Present() {
		if len(tokVal.Reveal()) < middleware.MinAdminTokenLen {
			return nil, fmt.Errorf("auth: MAKTABA_ADMIN_TOKEN is too short (need >= %d chars)", middleware.MinAdminTokenLen)
		}
		logger.Info("auth: admin-token bypass enabled", "event", "admin_token_enabled")
	}
	st.adminToken = tokVal

	// --- CORS (Story 10.15 AC-4) ---
	st.cors = httpsec.DefaultCORS()
	st.cors.AllowedOrigins = httpsec.ParseAllowedOrigins(os.Getenv("MAKTABA_CORS_ALLOWED_ORIGINS"))

	// --- Security headers (Story 10.15 AC-2, AC-5) ---
	st.headers = httpsec.DefaultHeaders()
	if os.Getenv("MAKTABA_HSTS") == "1" {
		st.headers.HSTS = httpsec.HSTSOneYear
	}

	return st, nil
}

// DefaultRotationOverlap reads `MAKTABA_JWT_ROTATION_OVERLAP_SEC` or
// returns the canonical 24h default. Exposed so tests and the
// serve-loop reaper can share the value.
func DefaultRotationOverlap() time.Duration {
	if v := os.Getenv("MAKTABA_JWT_ROTATION_OVERLAP_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}
	return keys.DefaultRotationOverlap
}

// applySecurity wraps a public-mux handler with the standard
// transport-security middleware stack:
//
//	headers → CORS → admin-token → JWT bearer → cookie-auth →
//	RequireAuthExcept(allowlist) → CSRF → next
//
// Order is load-bearing (read outermost-first as the request enters):
//
//   - headers run outermost so even an early 401/403 from the gate
//     still ships the standard CSP/HSTS.
//   - the three credential-attaching middlewares (admin-token, JWT,
//     cookie) run before the gate so a valid credential of ANY kind
//     has populated the principal by the time RequireAuthExcept
//     decides. admin-token precedes JWT so a single-user install can
//     short-circuit verification; cookie-auth runs last of the three
//     and no-ops when a principal is already attached.
//   - RequireAuthExcept is next: it turns "no principal" into 401 for
//     every path except the explicit public allowlist, finally
//     requiring auth on the business surface that was previously fully
//     anonymous.
//   - CSRF is innermost (closest to the handlers): by this point the
//     principal Source is known, so the double-submit guard can
//     cheaply skip bearer/admin-token clients and only challenge
//     state-changing ambient-cookie sessions (Story 10.10). Running it
//     AFTER the auth gate means an anonymous attacker hitting a
//     protected path already got a 401 and never reaches the CSRF
//     branch; the public login/refresh routes have no principal so
//     CSRF no-ops on them (a credential can still be issued).
//
// cookieAuth is the session-cookie principal middleware
// (auth.Handler.CookieAuth). csrf is the double-submit guard
// (auth.Handler.CSRF). Either may be nil when the Phase 9 auth surface
// is unwired (no DB / no keys); in that case the bearer/admin paths
// still gate the surface, cookie sessions simply aren't honoured, and
// there are no cookie sessions to CSRF-protect.
func (a *authState) applySecurity(
	next http.Handler,
	cookieAuth func(http.Handler) http.Handler,
	csrf func(http.Handler) http.Handler,
) http.Handler {
	stack := next
	if csrf != nil {
		stack = csrf(stack)
	}
	stack = middleware.RequireAuthExcept(middleware.DefaultPublicAllowlist())(stack)
	if cookieAuth != nil {
		stack = cookieAuth(stack)
	}
	stack = middleware.JWTBearer(a.keys, "api")(stack)
	stack = middleware.AdminToken(a.adminToken)(stack)
	stack = httpsec.CORS(a.cors)(stack)
	stack = httpsec.Headers(a.headers)(stack)
	return stack
}
