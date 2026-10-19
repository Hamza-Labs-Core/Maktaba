// Epic 33 (Maktaba TV staging, hot swap and change management) API wiring: the TV
// change endpoints (/api/tv/changes, /api/tv/releases, /api/tv/slots, /api/tv/rollback)
// and the change controller loop (story 33.10). The channel editor's and the TV
// settings' drafts are wired where those handlers are built (p27.go, p6.go) through
// tvchange.NewDrafts.
package router

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/go-chi/chi/v5"

	tvchangeapi "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tvchange"
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
}

// MountP33 attaches the TV change routes and starts the controller.
func MountP33(r chi.Router, d P33Deps) *tvchange.Controller {
	if d.DB == nil {
		return nil
	}
	ctl := tvchange.NewController(d.DB, d.Logger)
	tvchangeapi.New(d.DB, ctl).Mount(r)
	if d.RunController {
		ctx := d.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		go ctl.Run(ctx)
	}
	return ctl
}

// StagingInfo is /api/tv/info's staging block (README "APIs: Extended").
func StagingInfo(db *sql.DB) func(ctx context.Context) any {
	return func(ctx context.Context) any { return tvchangeapi.Info(ctx, db) }
}
