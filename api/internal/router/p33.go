// Epic 33 (Maktaba TV staging, hot swap and change management) API wiring: the TV
// change endpoints (/api/tv/changes, /api/tv/releases, /api/tv/slots, /api/tv/rollback)
// and the change controller loop (story 33.10). The channel editor's and the TV
// settings' drafts are wired where those handlers are built (p27.go, p6.go) through
// tvchange.NewDrafts.
//
// Story 33.13: the notification centre (/api/notifications, a general store whose first
// user is the TV change controller) is mounted here too, with the controller as its
// first producer.
package router

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/go-chi/chi/v5"

	tvchangeapi "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tvchange"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
)

// P33Deps bundles the dependencies of the Epic 33 handlers.
type P33Deps struct {
	DB     *sql.DB
	Logger *slog.Logger
	// RunController starts the change controller (one API process acts at a time:
	// it takes an advisory lock). Off in tests that only want the routes.
	RunController bool
	// Ctx bounds the controller loop (nil: the process's lifetime).
	Ctx context.Context
	// Notifier is where the controller's notices go (nil: a hub over DB whose first
	// subscriber is the notifications table; e-mail / push subscribe to it later).
	Notifier *notify.Hub
}

// MountP33 attaches the TV change routes and starts the controller.
func MountP33(r chi.Router, d P33Deps) *tvchange.Controller {
	if d.DB == nil {
		return nil
	}
	hub := d.Notifier
	if hub == nil {
		hub = notify.NewHub(d.DB, d.Logger)
	}
	ctl := tvchange.NewController(d.DB, d.Logger)
	ctl.Notifier = hub
	if d.RunController {
		// story 33.5: verification on the home front door's staging slot (a front
		// door without slots, or staging not built, keeps the database checks)
		ctl.Staging = tvchange.NewHTTPStaging(d.DB)
	}
	tvchangeapi.New(d.DB, ctl).Mount(r)
	(&notify.Handler{Store: &notify.Store{DB: d.DB}}).Mount(r)
	if d.RunController {
		ctx := d.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		safego.Go(d.Logger, "tvchange_controller", func() { ctl.Run(ctx) })
	}
	return ctl
}

// StagingInfo is /api/tv/info's staging block (README "APIs: Extended").
func StagingInfo(db *sql.DB) func(ctx context.Context) any {
	return func(ctx context.Context) any { return tvchangeapi.Info(ctx, db) }
}
