// Epic 27 (Live Channels) API handler wiring. Mounts the channel
// definition CRUD (Story 27.1) and the EPG / guide read surface (Story
// 27.4). Kept in its own file so the Epic 27 diff stays isolated,
// mirroring the p6/p9/p10/p11 convention.
//
// Channel CRUD mutations are admin-gated in-handler; reads are scoped to
// the caller's library ACL. The guide endpoints are pure read paths over
// channel_programs (Story 27.2's output).
package router

import (
	"database/sql"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/channels"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/guide"
)

// P27Deps bundles the dependencies for the Epic 27 handlers.
type P27Deps struct {
	DB *sql.DB
}

// MountP27 attaches the Epic 27 route handlers onto r. Safe to skip when
// DB is nil (no-DB stub); routes simply aren't registered.
func MountP27(r chi.Router, d P27Deps) {
	if d.DB == nil {
		return
	}
	chh := &channels.Handler{DB: d.DB}
	chh.Mount(r)
	chh.MountSchedule(r) // Story 27.2 — schedule read + regenerate trigger
	(&guide.Handler{DB: d.DB}).Mount(r)
}
