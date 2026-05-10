// Phase 9 / Epic 10 handler wiring. The auth surface lives in
// handlers/auth and depends on the auth/keys.Set bootstrapped in
// main.go's initAuth. Keeping the wiring in its own file makes the
// Phase 9 diff isolated and lets earlier phases boot without it.
package router

import (
	"database/sql"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/auth"
)

// P9Deps bundles the dependencies for the Phase 9 auth surface.
type P9Deps struct {
	DB            *sql.DB
	Keys          *keys.Set
	SecureCookies bool
	AccessTTL     time.Duration
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
	})
	h.Mount(r)
	return h
}
