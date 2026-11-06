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
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
)

// BuildInfo is the static metadata bundled into the binary at link time
// (or stubbed when running from `go run`).
type BuildInfo struct {
	Version string
	Commit  string
	BuiltAt string
	Role    string
	GoVer   string
}

// NewRouter returns a chi router with the standard middleware stack
// applied and the platform health endpoints registered. Callers (the
// api/relay role wiring) mount their own routes on top.
func NewRouter(logger *slog.Logger, pool *db.Pool, mig *db.Migrator, build BuildInfo, allowedOrigins []string, opts ...RouterOption) *chi.Mux {
	var o routerOptions
	for _, f := range opts {
		f(&o)
	}
	r := chi.NewRouter()
	r.Use(middleware.Recover(logger))
	r.Use(middleware.RequestID)
	r.Use(middleware.Logging(logger))
	r.Use(middleware.CORS(allowedOrigins))

	r.Get("/healthz", healthzHandler(build))
	ready := readyzHandler(pool, mig)
	if o.lenient != nil {
		ready = o.lenient.handler(pool, mig)
	}
	if o.draining != nil {
		ready = drainingReadyz(o.draining, ready)
	}
	r.Get("/readyz", ready)
	r.Get("/.well-known/maktaba-cloud-version", versionHandler(build))
	return r
}

func healthzHandler(build BuildInfo) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
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

// RouterOption configures NewRouter.
type RouterOption func(*routerOptions)

type routerOptions struct {
	lenient  *LenientReadiness
	draining func() bool
}

// WithDraining makes /readyz answer 503 "draining" while draining() is true:
// a relay moving its homes away (story 50.03) is taken out of the edge's
// rotation at once, so new tunnels and requests go to the other process
// (the rehearsal's edge and 50.08's blue/green deploy rely on it).
func WithDraining(draining func() bool) RouterOption {
	return func(o *routerOptions) { o.draining = draining }
}

func drainingReadyz(draining func() bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if draining() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "down", "reason": "draining"})
			return
		}
		next(w, r)
	}
}

// WithLenientReadiness gives /readyz the relay's rule (story 50.05): a
// database that is away for less than MaxOutage while the relay can still
// serve (Warm) answers 200 degraded, not 503, so the edge keeps sending it
// the homes it knows.
func WithLenientReadiness(l *LenientReadiness) RouterOption {
	return func(o *routerOptions) { o.lenient = l }
}

// LenientReadiness is the relay role's readiness.
type LenientReadiness struct {
	Warm      func() bool   // the relay knows homes it can serve without the database
	MaxOutage time.Duration // default 5 min
	now       func() time.Time

	mu        sync.Mutex
	downSince time.Time
}

// DownFor is how long the database has been unreachable (0: it is up).
func (l *LenientReadiness) DownFor() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.downSince.IsZero() {
		return 0
	}
	return l.clock().Sub(l.downSince)
}

func (l *LenientReadiness) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

func (l *LenientReadiness) handler(pool *db.Pool, mig *db.Migrator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		if err := pool.Ping(ctx); err != nil {
			l.mu.Lock()
			if l.downSince.IsZero() {
				l.downSince = l.clock()
			}
			down := l.clock().Sub(l.downSince)
			l.mu.Unlock()
			maxOutage := l.MaxOutage
			if maxOutage <= 0 {
				maxOutage = 5 * time.Minute
			}
			if down < maxOutage && l.Warm != nil && l.Warm() {
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "degraded", "db": "unreachable", "down_seconds": int(down.Seconds())})
				return
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "down", "reason": "db_unreachable", "error": err.Error()})
			return
		}
		l.mu.Lock()
		l.downSince = time.Time{}
		l.mu.Unlock()
		atHead, err := mig.AtHead(ctx)
		if err != nil || !atHead {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "down", "reason": "migrations_behind"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
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
	return func(w http.ResponseWriter, _ *http.Request) {
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
func DefaultOrigins(_ config.Config) []string {
	return []string{
		"https://app.maktaba.tv",
		"https://web.maktaba.tv",
		"https://admin.maktaba.tv",
		"https://maktaba.tv",
	}
}

// SetReadinessClock replaces l's clock (tests).
func SetReadinessClock(l *LenientReadiness, now func() time.Time) { l.now = now }
