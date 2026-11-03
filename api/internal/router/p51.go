// Epic 51 wiring (configurable components): the admin routes of the components
// (story 51.08). Later stories of the epic add their routes here.
//
//	/api/components…   home, admin-only
package router

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

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
	s.Tier = func() string { return ref.Store().TierName() }
	if ref != nil {
		// story 51.19: the desired rows say what the licence allows, now and on every
		// change of it (an apply, a refresh, a take-up, a revocation)
		publish := func(ctx context.Context) { publishLicences(ctx, s, ref.Store()) }
		s.AfterApply = publish
		ref.Store().OnChange(func() { publish(context.Background()) })
		publish(context.Background())
	}
	h := &componentsh.Handler{Service: s, DB: d.DB, Logger: d.Logger}
	h.Mount(r)
	return s
}

// publishLicences writes what lic allows on the desired rows; a database that does not
// answer is logged and tried again on the next change (the runners keep the last rows).
func publishLicences(ctx context.Context, s *components.Service, lic *subscriptions.Store) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := s.PublishLicences(ctx, lic); err != nil {
		l := s.Logger
		if l == nil {
			l = slog.Default()
		}
		l.Warn("components: what the licence allows cannot be published", "err", err.Error(),
			"event", "components_licence_publish_failed")
	}
}
