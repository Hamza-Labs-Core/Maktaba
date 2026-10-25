// Phase 9 / Epic 10 handler wiring. The auth surface lives in
// handlers/auth and depends on the auth/keys.Set bootstrapped in
// main.go's initAuth. Keeping the wiring in its own file makes the
// Phase 9 diff isolated and lets earlier phases boot without it.
package router

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/auth"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
)

// P9Deps bundles the dependencies for the Phase 9 auth surface.
type P9Deps struct {
	DB            *sql.DB
	Keys          *keys.Set
	SecureCookies bool
	AccessTTL     time.Duration

	// Seats is the live entitlement seat-cap source for Epic 16 Story
	// 16.2 enforcement on POST /api/users. Nil ⇒ the gate is inert
	// (free build / no entitlement source). Must be the SAME Store the
	// subscriptions surface uses so an apply/revoke is seen here too.
	Seats auth.SeatLimiter

	// Settings reads the home's name (home.name) for GET /api/auth/me: the
	// reader PATCH /api/settings drops the cache of (P6Deps.Settings). Nil ⇒
	// one of its own (a rename then reads within its cache's few seconds).
	Settings *runtimecfg.Reader
}

// MountP9 attaches every Phase 9 (Epic 10 stories 10.2/10.3/10.4/10.5/10.16)
// handler onto r. Safe to call with a nil DB or nil Keys — handlers
// return 503 in those cases via the underlying Store's error paths.
//
// The returned *auth.Handler is also exposed so callers (main.go) can
// install the cookie-auth middleware globally (handler.CookieAuth) ahead
// of route handlers that want a session-backed principal.
func MountP9(r chi.Router, d P9Deps) *auth.Handler {
	if d.DB == nil || d.Keys == nil {
		return nil
	}
	h := auth.NewHandler(auth.Deps{
		DB:            d.DB,
		Keys:          d.Keys,
		SecureCookies: d.SecureCookies,
		AccessTTL:     d.AccessTTL,
		Seats:         d.Seats,
	})
	settings := d.Settings
	if settings == nil {
		settings = runtimecfg.New(d.DB)
	}
	h.HomeName = HomeName(settings, cluster.ComputerName)
	h.Mount(r)
	return h
}

// HomeName is the home's name: the one an admin stored (home.name), else the
// computer's name (asked once, on the first read).
func HomeName(settings *runtimecfg.Reader, computer func() string) func(context.Context) string {
	fallback := sync.OnceValue(computer)
	return func(ctx context.Context) string {
		if n, ok := settings.Text(ctx, runtimecfg.HomeName); ok {
			return n
		}
		return fallback()
	}
}
