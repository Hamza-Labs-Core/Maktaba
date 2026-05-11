// Package server assembles the HTTP listener for the api and relay
// roles. Each role has a distinct chi router so the middleware order
// and route surface stay separate; both share the same health probes
// and graceful-shutdown machinery.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
)

// BuildInfo is the static metadata bundled into the binary at link time
// (or stubbed when running from `go run`).
type BuildInfo struct {
	Version  string
	Commit   string
	BuiltAt  string
	Role     string
	GoVer    string
}

// NewRouter returns a chi router with the standard middleware stack
// applied and the platform health endpoints registered. Callers (the
// api/relay role wiring) mount their own routes on top.
func NewRouter(logger *slog.Logger, pool *db.Pool, mig *db.Migrator, build BuildInfo, allowedOrigins []string) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Recover(logger))
	r.Use(middleware.RequestID)
	r.Use(middleware.Logging(logger))
	r.Use(middleware.CORS(allowedOrigins))

	r.Get("/healthz", healthzHandler(build))
	r.Get("/readyz", readyzHandler(pool, mig))
	r.Get("/.well-known/maktaba-cloud-version", versionHandler(build))
	return r
}

func healthzHandler(build BuildInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":   "ok",
			"version":  build.Version,
			"commit":   build.Commit,
			"role":     build.Role,
			"built_at": build.BuiltAt,
		})
	}
}

func readyzHandler(pool *db.Pool, mig *db.Migrator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		if err := pool.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "down", "reason": "db_unreachable", "error": err.Error()})
			return
		}
		atHead, err := mig.AtHead(ctx)
		if err != nil || !atHead {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "down", "reason": "migrations_behind"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	}
}

func versionHandler(build BuildInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"version":  build.Version,
			"commit":   build.Commit,
			"built_at": build.BuiltAt,
			"go":       build.GoVer,
		})
	}
}

// DefaultOrigins is the locked allow-list used by all roles unless the
// operator passes their own through config in a future story.
func DefaultOrigins(cfg config.Config) []string {
	return []string{
		"https://app.maktaba.app",
		"https://web.maktaba.app",
		"https://admin.maktaba.app",
		"https://maktaba.app",
	}
}
