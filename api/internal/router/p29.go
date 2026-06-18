// Epic 29 (Watch Analytics) API handler wiring. Mounts the watch-session
// lifecycle + history + activity surface (Stories 29.1, 29.2, 29.4) and
// the admin analytics dashboard + per-video stats + export (29.3, 29.5,
// 29.6). Kept in its own file so the Epic 29 diff stays isolated,
// mirroring the p6/p9/p10/p11/p27/p28 convention.
//
// All routes ride the principal middleware applySecurity installs:
//   - the /api/watch/* and /api/me/* routes require an authenticated
//     principal and are owner-scoped in-handler;
//   - the /api/admin/analytics/* routes are admin-gated in-handler;
//   - /api/videos/{id}/stats is authenticated, with the per-user
//     breakdown gated to admins in-handler.
//
// The session reaper goroutine is NOT started here — main.go owns its
// lifetime (it needs a background context), the same split p28 uses for
// the update poller.
package router

import (
	"database/sql"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/analytics"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/watch"
)

// P29Deps bundles the dependencies for the Epic 29 handlers.
type P29Deps struct {
	DB *sql.DB

	// Driver selects the analytics dialect ("postgres" default, "sqlite"
	// for the unified home-server binary).
	Driver string
}

// MountP29 attaches the Epic 29 route handlers onto r. Safe to skip when
// DB is nil (no-DB stub); routes simply aren't registered.
func MountP29(r chi.Router, d P29Deps) {
	if d.DB == nil {
		return
	}
	(&watch.Handler{DB: d.DB}).Mount(r)
	(&analytics.Handler{DB: d.DB, Driver: d.Driver}).Mount(r)
}
