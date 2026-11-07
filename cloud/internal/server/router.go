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
	if o.lenient != nil {
		r.Get("/readyz", o.lenient.handler(pool, mig))
	} else {
		r.Get("/readyz", readyzHandler(pool, mig))
	}
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

type routerOptions struct{ lenient *LenientReadiness }

// WithLenientReadiness gives /readyz the relay's rule (story 50.05): a
// database that is away for less than MaxOutage while the relay can still
// serve (Warm) answers 200 degraded, not 503, so the edge keeps sending it
// the homes it knows.
func WithLenientReadiness(l *LenientReadiness) RouterOption {
	return func(o *routerOptions) { o.lenient = l }
}

// LenientReadiness is the relay role's readiness. Its database checks run in
// the background (every second) and /readyz answers from the last one at
// once: a probe never waits on a database that is away (lib/pq's connect does
// not stop with its context, so a ping of a vanished database takes its whole
// connect timeout, longer than the edge's health check: found by the
// rehearsal's db-blip, where both relay processes left the edge's rotation).
type LenientReadiness struct {
	Warm      func() bool   // the relay knows homes it can serve without the database
	MaxOutage time.Duration // default 5 min
	Every     time.Duration // the background check's period (default 1 s)
	now       func() time.Time

	once      sync.Once
	mu        sync.Mutex
	up        bool
	atHead    bool
	lastErr   string
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

// check pings the database and, when it answers, checks the migrations.
func (l *LenientReadiness) check(pool *db.Pool, mig *db.Migrator) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := pool.Ping(ctx)
	atHead := false
	if err == nil {
		atHead, _ = mig.AtHead(ctx)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.up, l.atHead = err == nil, atHead
	if err != nil {
		l.lastErr = err.Error()
		if l.downSince.IsZero() {
			l.downSince = l.clock()
		}
	} else {
		l.lastErr, l.downSince = "", time.Time{}
	}
}

func (l *LenientReadiness) start(pool *db.Pool, mig *db.Migrator) {
	l.once.Do(func() {
		l.check(pool, mig)
		go func() {
			every := l.Every
			if every <= 0 {
				every = time.Second
			}
			tk := time.NewTicker(every)
			defer tk.Stop()
			for range tk.C {
				l.check(pool, mig)
			}
		}()
	})
}

func (l *LenientReadiness) handler(pool *db.Pool, mig *db.Migrator) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		l.start(pool, mig)
		w.Header().Set("Content-Type", "application/json")
		l.mu.Lock()
		up, atHead, lastErr := l.up, l.atHead, l.lastErr
		var down time.Duration
		if !l.downSince.IsZero() {
			down = l.clock().Sub(l.downSince)
		}
		l.mu.Unlock()
		if !up {
			maxOutage := l.MaxOutage
			if maxOutage <= 0 {
				maxOutage = 5 * time.Minute
			}
			if down < maxOutage && l.Warm != nil && l.Warm() {
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "degraded", "db": "unreachable", "down_seconds": int(down.Seconds())})
				return
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "down", "reason": "db_unreachable", "error": lastErr})
			return
		}
		if !atHead {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "down", "reason": "migrations_behind"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	}
}

// Recheck runs the background check now (tests).
func (l *LenientReadiness) Recheck(pool *db.Pool, mig *db.Migrator) { l.check(pool, mig) }

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
