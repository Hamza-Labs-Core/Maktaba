// Epic 51 wiring (configurable components): the admin routes of the components
// (story 51.08) and the capabilities every user reads (story 51.09). Later stories of
// the epic add their routes here.
//
//	/api/components…   home, admin-only
//	/api/capabilities  home, every signed-in user
package router

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	componentsh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws/eventbus"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// P51Deps bundles the Epic 51 routes.
type P51Deps struct {
	DB *sql.DB
	// License gates a component that names a licence flag (nil: the free tier).
	License *subscriptions.StoreRef
	Logger  *slog.Logger
	// Capabilities is the snapshot router.Deps gave the component_off check (nil: one
	// of its own). MountP51 keeps it in step with the desired state and the licence.
	Capabilities *components.Capabilities
	// DSN is the database for the capabilities' change listener (empty: the 30 s poll).
	DSN string
	// Bus carries capabilities_changed to every replica's /ws/home (nil: Hub only).
	Bus *eventbus.Bus
	// Hub is this replica's fan-out, used when there is no bus (nil: no push).
	Hub *ws.Hub
	// RunCapabilities starts the capabilities' watcher and push (main.go; tests mount
	// the routes only).
	RunCapabilities bool
	// Packs is this machine's pack installer (nil: the supervisor named by
	// MAKTABA_SUPERVISOR_URL and MAKTABA_SUPERVISOR_TOKEN_FILE, if any).
	Packs *componentsh.Packs
}

// MountP51 mounts the components' admin routes and GET /api/capabilities (nil DB:
// nothing) and answers the service.
func MountP51(r chi.Router, d P51Deps) *components.Service {
	if d.DB == nil {
		return nil
	}
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
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
	pk := d.Packs
	if pk == nil {
		pk = componentsh.PacksFromEnv()
	}
	h := &componentsh.Handler{Service: s, DB: d.DB, Logger: d.Logger, Packs: pk}
	h.Mount(r)

	caps := d.Capabilities
	if caps == nil {
		caps = components.NewCapabilities(components.CapabilitiesOptions{Logger: logger,
			AllOn: components.CapabilitiesGateOff()})
	}
	caps.SetLicensed(s.Licensed)
	(&componentsh.Capabilities{Caps: caps}).Mount(r)
	if d.RunCapabilities {
		components.StartCapabilities(caps, s.Store, store.IsSQLite(d.DB), d.DSN,
			CapabilitiesPublisher(d.DB, d.Bus, d.Hub), logger)
	}
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

// CapabilitiesPublisher pushes capabilities_changed {etag} on /ws/home: through the bus
// (every replica's clients, replayed on reconnect), else to hub. Each replica sees the
// same change, so one whose ETag is already the channel's last event does not send it
// again. nil bus and hub: nil.
func CapabilitiesPublisher(db *sql.DB, bus *eventbus.Bus, hub *ws.Hub) components.Publisher {
	switch {
	case bus != nil:
		return func(ctx context.Context, etag string) error {
			// one replica at a time looks and sends: the lock is held until the
			// event is written, so the next one sees it
			conn, err := db.Conn(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext('capabilities_changed'))`); err != nil {
				return err
			}
			defer func() {
				_, _ = conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext('capabilities_changed'))`)
			}()
			var last sql.NullString
			err = conn.QueryRowContext(ctx, `SELECT payload->>'etag' FROM events
				WHERE channel = $1 AND type = $2 ORDER BY id DESC LIMIT 1`,
				ws.HomeChannel, ws.EventCapabilitiesChanged).Scan(&last)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if last.Valid && last.String == etag {
				return nil
			}
			return bus.Publish(ctx, ws.HomeChannel, ws.EventCapabilitiesChanged, map[string]any{"etag": etag})
		}
	case hub != nil:
		return func(ctx context.Context, etag string) error {
			ws.PublishFromCtx(ctx, hub, ws.HomeChannel, ws.EventCapabilitiesChanged, map[string]any{"etag": etag})
			return nil
		}
	}
	return nil
}
