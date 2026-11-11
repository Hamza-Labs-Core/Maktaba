// Epic 57 story 57.01 wiring (age tiers): the admin's age-tier routes, the ratings
// table every signed-in user reads, and the recompute that writes a video's tier.
//
//	PUT /api/{videos,libraries,series,collections,channels}/{id}/age-tier   home, admin
//	PUT /api/libraries/{id}/youtube/sources/{sid}/age-tier                  home, admin
//	GET /api/parental/ratings                                               home, signed in
package router

import (
	"database/sql"
	"log/slog"

	"github.com/go-chi/chi/v5"

	parentalh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// P57TiersDeps bundles the age tiers' routes.
type P57TiersDeps struct {
	DB     *sql.DB
	Logger *slog.Logger
}

// MountP57Tiers mounts the age-tier routes (nil DB: nothing) and answers the
// recompute: main.go runs it as the parental_recompute loop and hands it to whatever
// else changes what a tier is resolved from.
func MountP57Tiers(r chi.Router, d P57TiersDeps) *parental.Recomputer {
	if d.DB == nil {
		return nil
	}
	rc := parental.NewRecomputer(d.DB, d.Logger)
	(&parentalh.TiersHandler{DB: d.DB, Recompute: rc, Logger: d.Logger}).Mount(r)
	return rc
}
