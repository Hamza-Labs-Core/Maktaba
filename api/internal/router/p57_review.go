// Epic 57 story 57.17 wiring (tiers detected from transcripts): the review list and
// the admin's answer.
//
//	GET  /api/parental/review              home, admin or guardian
//	POST /api/parental/review/{video_id}   home, admin
package router

import (
	"database/sql"
	"log/slog"

	"github.com/go-chi/chi/v5"

	parentalh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// P57ReviewDeps bundles the review routes.
type P57ReviewDeps struct {
	DB *sql.DB
	// Recompute is MountP57Tiers' answer: the one writer of a video's tier.
	Recompute *parental.Recomputer
	Logger    *slog.Logger
}

// MountP57Review mounts the review routes. Without a database or the recompute it
// mounts nothing: a review that could not rate the video again is not offered.
func MountP57Review(r chi.Router, d P57ReviewDeps) {
	if d.DB == nil || d.Recompute == nil {
		return
	}
	(&parentalh.ReviewHandler{DB: d.DB, Recompute: d.Recompute, Logger: d.Logger}).Mount(r)
}
