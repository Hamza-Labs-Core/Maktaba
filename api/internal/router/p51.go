// Epic 51 wiring (configurable components): the admin routes of the components
// (story 51.08). Later stories of the epic add their routes here.
//
//	/api/components…   home, admin-only
package router

import (
	"database/sql"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	componentsh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// P51Deps bundles the Epic 51 routes.
type P51Deps struct {
	DB *sql.DB
	// License gates a component that names a licence flag (nil: the free tier).
	License *subscriptions.StoreRef
	Logger  *slog.Logger
}

// MountP51 mounts the components' admin routes (nil DB: nothing) and answers the service.
func MountP51(r chi.Router, d P51Deps) *components.Service {
	if d.DB == nil {
		return nil
	}
	s := components.NewService(d.DB)
	s.Logger = d.Logger
	ref := d.License
	s.Licensed = func(flag string) bool { return ref.Store().Allows(subscriptions.Feature(flag)) }
	h := &componentsh.Handler{Service: s, DB: d.DB, Logger: d.Logger}
	h.Mount(r)
	return s
}
