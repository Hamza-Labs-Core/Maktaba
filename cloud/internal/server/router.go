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
	"net/netip"
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
	switch {
	case o.clientIP:
		// before the logger, so its remote_ip is the resolved client (50.31)
		r.Use(middleware.WithClientIP(o.trusted))
	case o.resolveIP != nil:
		// the relay's own rule (its rightmost X-Forwarded-For entry from Caddy), for the log
		r.Use(middleware.WithResolvedClientIP(o.resolveIP))
	}
	r.Use(middleware.Logging(logger))
	if o.hosts != nil {
		// the user listener answers its own names only (50.32 §1); its JSON carries §5's headers
		r.Use(middleware.OnlyHosts(logger, "user", o.hosts))
		r.Use(middleware.APIHeaders)
	}
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
	lenient   *LenientReadiness
	draining  func() bool
	clientIP  bool
	trusted   []netip.Prefix
	resolveIP func(*http.Request) string
	hosts     []string // non-nil: the api's user listener (50.32)
}

// WithClientIPFrom stores resolve(r) as the request's client IP before the access log, for
// a role with its own rule: the relay's rightmost X-Forwarded-For entry, which Caddy appends
// (Epic 50 story 50.32 §1, after 50.31 stopped the log from printing the leftmost entry).
func WithClientIPFrom(resolve func(*http.Request) string) RouterOption {
	return func(o *routerOptions) { o.resolveIP = resolve }
}

// WithUserHosts makes the router the api's user listener (Epic 50 story 50.32): a Host not in
// hosts is 404 (any Host when hosts is empty: development), and every answer carries the
// API's headers.
func WithUserHosts(hosts []string) RouterOption {
	return func(o *routerOptions) {
		o.hosts = append([]string{}, hosts...)
	}
}

// WithTrustedProxies resolves each request's client IP (middleware.WithClientIP) before
// anything else reads it, the access log included: CF-Connecting-IP from a peer in trusted,
// else the TCP peer (Epic 50 story 50.31). The api role sets it from
// server.trusted_proxies; the relay never does.
func WithTrustedProxies(trusted []netip.Prefix) RouterOption {
	return func(o *routerOptions) { o.clientIP, o.trusted = true, trusted }
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
// operator passes their own through config in a future story. The operator console's
// origin is not in it: its listener is same-origin only (story 50.32).
func DefaultOrigins(_ config.Config) []string {
	return []string{
		"https://app.maktaba.tv",
		"https://web.maktaba.tv",
		"https://maktaba.tv",
	}
}

// SetReadinessClock replaces l's clock (tests).
func SetReadinessClock(l *LenientReadiness, now func() time.Time) { l.now = now }
