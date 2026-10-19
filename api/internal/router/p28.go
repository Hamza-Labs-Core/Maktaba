// Epic 28 (Auto-Update) API handler wiring. Mounts the public
// update-check endpoint (Story 28.2) and the admin self-update endpoint
// (Story 28.3). Kept in its own file so the Epic 28 diff stays isolated,
// mirroring the p6/p9/p10/p11/p27 convention.
//
//	GET  /api/system/updates        — public; channel-aware update status
//	POST /api/admin/system/update   — admin-only; download + verify + swap
//
// The admin gate is enforced in-handler via principal.FromContext (same
// pattern as the channel mutations), so this file only needs the
// principal middleware that applySecurity already wraps the whole mux
// with — no per-route auth wiring here.
package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/system"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/updates"
)

// P28Deps bundles the dependencies for the Epic 28 handlers.
type P28Deps struct {
	// Updater is the shared update-check service (its background loop is
	// started by main.go). nil skips all Epic 28 routes.
	Updater *system.Updater
	// Updates is the Epic 35 updater (story 35.3), filled in by MountP35 once the
	// database is open. Set: GET /api/system/updates gains verified / policy /
	// next_run_at, and on builds that install by themselves POST
	// /api/admin/system/update is Update now (returns {run_id}).
	Updates *UpdatesRef
}

// MountP28 attaches the Epic 28 routes onto r. Safe to skip when Updater
// is nil (no-op), keeping a bare-Deps unit test unaffected.
func MountP28(r chi.Router, d P28Deps) {
	if d.Updater == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(mw.BodyLimit(mw.DefaultBodyLimit))
		status := d.Updater.Handler()
		self := system.NewSelfUpdater(d.Updater).Handler()
		r.Method(http.MethodGet, "/api/system/updates", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if h := d.Updates.Get(); h != nil {
				h.ExtendStatus(status).ServeHTTP(w, req)
				return
			}
			status.ServeHTTP(w, req)
		}))
		r.Method(http.MethodPost, "/api/admin/system/update", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if h := d.Updates.Get(); h != nil && updates.AutoInstallAvailable() {
				h.AdminUpdateHandler().ServeHTTP(w, req)
				return
			}
			self.ServeHTTP(w, req)
		}))
	})
}
