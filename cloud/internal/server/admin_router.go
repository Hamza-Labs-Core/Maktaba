package server

import (
	"log/slog"
	"net/netip"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
)

// NewAdminRouter is the operator console's listener (Epic 50 story 50.32 §1): it answers
// exactly host (admin.maktaba.tv; anything else is 404), allows no cross-origin request
// (a preflight is 403), refuses a state-changing request without origin and
// X-Maktaba-Staff: 1 (403 bad_origin), and carries the console's headers. The client IP is
// resolved as on the user listener. Only /healthz and /readyz are registered here: the
// caller mounts /v1/admin/* (and 50.33's /v1/staff/*) on top, and nothing else.
func NewAdminRouter(logger *slog.Logger, pool *db.Pool, mig *db.Migrator, build BuildInfo, host, origin string, trusted []netip.Prefix) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Recover(logger))
	r.Use(middleware.RequestID)
	r.Use(middleware.WithClientIP(trusted))
	r.Use(middleware.Logging(logger))
	r.Use(middleware.OnlyHosts(logger, "admin", []string{host}))
	r.Use(middleware.AdminHeaders)
	r.Use(middleware.AdminSameOrigin(origin))
	r.Get("/healthz", healthzHandler(build))
	r.Get("/readyz", readyzHandler(pool, mig))
	return r
}

// AdminPathPrefixes are the paths only the admin listener serves; the user listener has no
// route under them (story 50.32 T03).
var AdminPathPrefixes = []string{"/v1/admin/", "/v1/staff/"}

// IsAdminPath reports whether p is one of the admin listener's paths.
func IsAdminPath(p string) bool {
	for _, pre := range AdminPathPrefixes {
		if len(p) >= len(pre) && p[:len(pre)] == pre {
			return true
		}
	}
	return p == "/v1/admin" || p == "/v1/staff"
}
