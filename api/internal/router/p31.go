// Epic 31 (multi-instance Maktaba) API wiring: the home's identity, the
// machines that join it and their database roles
// (specs/epics/31-multi-instance/design.md §8). Kept in its own file,
// mirroring the p6/p9/p10/p11/p27/p28/p29 convention.
//
//	POST /api/cluster/join, GET /api/cluster/join/{node_id} — public
//	  (allowlisted in auth/middleware), rate-limited per IP in-handler;
//	everything else under /api/cluster — admin-gated in-handler.
//
// The identity bootstrap, the mDNS announcement and the housekeeping loop
// are started by main.go with the returned handler.
package router

import (
	"database/sql"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	clusterh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/cluster"
)

// P31Deps bundles the dependencies for the Epic 31 handlers.
type P31Deps struct {
	DB *sql.DB
	// Driver is "postgres" (default) or "sqlite". A SQLite home cannot take
	// machines: it keeps its identity in memory and reports why joins are
	// unavailable.
	Driver     string
	Advertiser *cluster.Advertiser
	// PGPort is the database port handed to machines (0: 5432).
	PGPort int
	Logger *slog.Logger
}

// MountP31 attaches the Epic 31 routes onto r and returns the handler
// (nil when DB is nil and the home is not SQLite).
func MountP31(r chi.Router, d P31Deps) *clusterh.Handler {
	h := &clusterh.Handler{Advertiser: d.Advertiser, PGPort: d.PGPort, Logger: d.Logger}
	switch {
	case d.Driver == "sqlite":
		h.Store = cluster.NewMemoryStore()
		h.Roles = cluster.Unsupported{Reason: cluster.ReasonSQLite}
	case d.DB != nil:
		h.Store = &cluster.PGStore{DB: d.DB}
		h.Roles = &cluster.PGRoles{DB: d.DB}
	default:
		return nil
	}
	h.Mount(r)
	return h
}
