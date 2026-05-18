// Package main is the entry point for the Maktaba API server.
//
// Story 22.1 created the binary as a stub; Story 22.2 wired the
// `--version` flag and reproducibility envelope; Story 22.3 added the
// `serve` subcommand so compose has a long-lived process to attach a
// healthcheck to; Story 22.4 added the `migrate` subcommand; Story
// 21.1 wired the structured logger; Story 21.4 split serve into a
// public mux (port 8080, /api/system/health aggregator) and an admin
// mux (port 9100, /healthz + /readyz). Story 7.1 replaces the public
// mux's placeholder route with the chi-based real router; Story 21.2
// adds /metrics on the admin port; Story 21.3 wires opt-in OTel
// tracing. Stories 10.1/10.6/10.9/10.15 add the auth bootstrap
// (`adduser`, `keys`, JWKS endpoint, security middleware stack).
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	grpcpipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
	grpcstreaming "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/streaming"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/perf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/system"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
	errrpt "github.com/Hamza-Labs-Core/Maktaba/shared/errrpt/go"
	health "github.com/Hamza-Labs-Core/Maktaba/shared/health/go"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
	metrics "github.com/Hamza-Labs-Core/Maktaba/shared/metrics/go"
	tracing "github.com/Hamza-Labs-Core/Maktaba/shared/tracing/go"
)

// shutdownGrace is the total budget for in-flight requests to drain
// before the process exits. Story 7.1 AC-3: default 30 s, overridable
// via $MAKTABA_SHUTDOWN_GRACE.
const defaultShutdownGrace = 30 * time.Second

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--version":
			fmt.Fprintln(os.Stdout, version.String())
			return
		case "migrate":
			if err := runMigrate(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
				os.Exit(1)
			}
			return
		case "serve":
			if err := runServe(); err != nil {
				fmt.Fprintf(os.Stderr, "serve: %v\n", err)
				os.Exit(1)
			}
			return
		case "adduser":
			if err := runAddUser(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "adduser: %v\n", err)
				os.Exit(1)
			}
			return
		case "keys":
			if err := runKeys(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "keys: %v\n", err)
				os.Exit(1)
			}
			return
		case "help", "-h", "--help":
			printUsage(os.Stdout)
			return
		}
	}

	logger := initLogger()
	logger.Info("starting maktaba-api",
		"commit", version.Commit,
		"build_date", version.BuildDate,
		"event", "startup",
	)
	logger.Info("stub server: pass `serve` to launch the API, or `migrate` to apply migrations")
}

func initLogger() *slog.Logger {
	return mlog.Init(mlog.Options{
		Service: "api",
		Env:     env(),
		Version: version.Version,
	})
}

func env() string {
	if v := os.Getenv("MAKTABA_ENV"); v != "" {
		return v
	}
	return "dev"
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, `maktaba-api — Maktaba server binary

Usage:
  maktaba-api <command> [flags]

Commands:
  serve      Run the HTTP server (chi router + admin port + /metrics)
  migrate    Run database migrations (see "migrate --help")
  adduser    Seed the first admin user (Story 10.1 AC-4)
  keys       Generate or rotate RS256 JWT keys (Story 10.6)
  --version  Print the binary's version
  help       Show this help`)
}

// runServe stands up three HTTP servers:
//
//   - Public on $MAKTABA_HTTP_ADDR (default :8080) carrying the API's
//     user-facing routes (chi router; Story 7.1).
//   - Admin on $MAKTABA_ADMIN_ADDR (default :9100) carrying the
//     orchestrator's probes (/healthz + /readyz; Story 21.4) and
//     /metrics (Story 21.2 — bound to the admin port so a misconfigured
//     ingress can't expose it without explicit operator action).
//   - When $MAKTABA_METRICS_PUBLIC_ADDR is set, /metrics also runs on
//     that address with a bearer token. Off by default (Story 21.2 AC-4).
//
// All three share a graceful-shutdown context so SIGINT / SIGTERM
// drains them together.
func runServe() error {
	logger := initLogger()

	// MAKTABA_AUTO_MIGRATE=true runs `migrate up` synchronously before
	// the HTTP servers bind. The dev/e2e compose stacks set this so
	// downstream services (pipeline's reaper, streaming's session
	// claim) don't race the schema and crash with
	// `UndefinedTableError: relation "processing_jobs" does not exist`
	// before the operator has hand-run `make migrate`.
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MAKTABA_AUTO_MIGRATE")), "true") {
		logger.Info("auto_migrate_starting", "event", "auto_migrate_starting")
		if err := runMigrate([]string{"up"}); err != nil {
			return fmt.Errorf("auto-migrate: %w", err)
		}
		logger.Info("auto_migrate_complete", "event", "auto_migrate_complete")
	}

	publicAddr := os.Getenv("MAKTABA_HTTP_ADDR")
	if publicAddr == "" {
		publicAddr = ":8080"
	}
	adminAddr := os.Getenv("MAKTABA_ADMIN_ADDR")
	if adminAddr == "" {
		adminAddr = ":9100"
	}

	// OTel tracing — opt-in. Empty endpoint = noop tracer (no outbound
	// connections). Story 21.3 AC-4.
	traceShutdown, err := tracing.Init(context.Background(), tracing.Config{
		Service:      "api",
		Env:          env(),
		Version:      version.Version,
		OTLPEndpoint: os.Getenv("MAKTABA_OTLP_ENDPOINT"),
		// Self-hosters running a local collector on loopback set
		// MAKTABA_OTLP_INSECURE=1 to skip TLS. Default keeps TLS on.
		OTLPInsecure: os.Getenv("MAKTABA_OTLP_INSECURE") == "1",
		SampleRatio:  0.01,
	})
	if err != nil {
		logger.Warn("tracing init failed; continuing with noop tracer",
			"err", err, "event", "tracing_init_failed")
	}

	// Shared error-reporting surface (HLB-300). One Reporter for the
	// process, mirroring how tracing is constructed once here. The
	// webhook sink is opt-in via $MAKTABA_ERROR_WEBHOOK: unset => nil
	// sink => default-off (no outbound, never fails), exactly the
	// errrpt package's documented behaviour. Capture still mints the
	// error_id and logs structured locally. The sink drops rather than
	// queues, so there is nothing to flush/close on shutdown.
	errReporter := errrpt.New(logger,
		errrpt.NewWebhookSink(os.Getenv("MAKTABA_ERROR_WEBHOOK")))

	checks := buildChecks(logger)
	warm := warmPeriod()
	adminMux := health.AdminMux("api", checks, warm)

	// /metrics on admin port. The handler is bound by NewHandler with
	// Public=false (no bearer required) because the admin port is
	// already firewalled in production deploys.
	mh, err := metrics.NewHandler(metrics.Config{Bind: adminAddr})
	if err != nil {
		return fmt.Errorf("metrics handler: %w", err)
	}
	adminMux.Handle("/metrics", mh)

	// Auth bootstrap (Stories 10.6/10.9/10.15) — keys, admin-token,
	// CORS, security headers. Tolerates fully-unset env (dev stub).
	auth, err := initAuth(logger)
	if err != nil {
		return err
	}

	// Public router (chi). The aggregator + version routes live inside
	// router.New; later stories mount business handlers here.
	//
	// Idempotency store: Postgres-backed when DATABASE_URL is set so
	// retried mutations replay correctly across restarts and replicas
	// (HLB-315); falls back to the in-memory store otherwise (dev /
	// no-DB stub). The Postgres store opens its own small pool for the
	// same isolation reason P6 does — the replay path must not contend
	// with handler or readiness pools.
	idemStore, idemSweep, idemClose := buildIdempotencyStore(logger)
	go idemSweep()
	defer idemClose()

	r := router.New(router.Deps{
		IdempotencyStore:   idemStore,
		IPRatePerMin:       envIntDefault("MAKTABA_IP_RATE_PER_MIN", 6000),
		UserRatePerMin:     envIntDefault("MAKTABA_USER_RATE_PER_MIN", 600),
		SchemaRev:          envIntDefault("MAKTABA_SCHEMA_REV", 0),
		AggregatorServices: buildAggregatorServices(),
		ErrorReporter:      errReporter,
	})

	// Phase 6 (Epic 7) handler wiring. Opens its own *sql.DB so the
	// admin-port readiness check and the handler DB pool are
	// independent — a starved readiness pool can't take down user
	// traffic and vice versa. A missing DATABASE_URL leaves the P6
	// surface unwired (the routes return 404 from chi as before).
	// cookieAuth is the session-cookie principal middleware, captured
	// from the Phase 9 handler so applySecurity can install it ahead of
	// the auth gate. Nil when the auth surface is unwired (no DB/keys).
	var cookieAuth func(http.Handler) http.Handler
	var csrf func(http.Handler) http.Handler
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		appDB, dbErr := sql.Open("postgres", dsn)
		if dbErr != nil {
			logger.Warn("p6: sql.Open failed; handlers unwired",
				"err", dbErr, "event", "p6_db_open_failed")
		} else {
			// Story 18.7 / HLB-346: route pool sizing through the
			// canonical perf.ApplyPool helper instead of bypassing it
			// with raw setters. Env overrides preserve operator control;
			// the perf package's tuned defaults/timeouts now actually
			// apply (previously dead code).
			poolCfg := perf.DefaultPoolConfig()
			poolCfg.MaxOpen = envIntDefault("MAKTABA_DB_MAX_OPEN", 32)
			poolCfg.MaxIdle = envIntDefault("MAKTABA_DB_MAX_IDLE", 8)
			if perr := perf.ApplyPool(appDB, poolCfg); perr != nil {
				logger.Warn("p6: perf.ApplyPool rejected config; falling back to raw setters",
					"err", perr, "event", "p6_pool_cfg_invalid")
				appDB.SetMaxOpenConns(poolCfg.MaxOpen)
				appDB.SetMaxIdleConns(poolCfg.MaxIdle)
			}

			// Shared perf cache registry (Story 18.8 AC4 / HLB-346):
			// hot-path caches register here so POST /admin/cache/{name}/
			// flush can drop them. Previously MountP10 always built an
			// empty registry, so every flush 404'd.
			perfRegistry := perf.NewRegistry()

			pipelineAddr := os.Getenv("MAKTABA_PIPELINE_ADDR")
			streamingAddr := os.Getenv("MAKTABA_STREAMING_ADDR")
			var pipelineClient grpcpipeline.Client
			var streamingClient grpcstreaming.Client
			if pipelineAddr != "" {
				cfg := grpcpipeline.DefaultConfig()
				cfg.Addr = pipelineAddr
				pipelineClient = grpcpipeline.NewRealClient(cfg)
				logger.Info("p6: pipeline gRPC client wired",
					"addr", pipelineAddr, "event", "p6_pipeline_wired")
			}
			if streamingAddr != "" {
				cfg := grpcstreaming.DefaultConfig()
				cfg.Addr = streamingAddr
				streamingClient = grpcstreaming.NewRealClient(cfg)
				logger.Info("p6: streaming gRPC client wired",
					"addr", streamingAddr, "event", "p6_streaming_wired")
			}

			// Cross-replica WS event bus lifetime (Epic 19 Story 19.2 /
			// HLB-353). Bound to its own context so the LISTEN loop +
			// pruner stop on process exit; cancelled via defer so a
			// panic in later bootstrap still tears the listener down.
			busCtx, busCancel := context.WithCancel(context.Background())
			defer busCancel()

			router.MountP6(r, router.P6Deps{
				DB:              appDB,
				PipelineClient:  pipelineClient,
				StreamingClient: streamingClient,
				BusCtx:          busCtx,
				BusDSN:          dsn,
				PerfRegistry:    perfRegistry,
			})
			logger.Info("p6: handlers mounted", "event", "p6_mounted")

			// Phase 9 (Epic 10 stories 10.2-10.5, 10.16) — auth surface.
			// Reuses the same app DB. SecureCookies tracks MAKTABA_HSTS
			// as a reasonable proxy for "we're behind TLS"; operators
			// can opt in/out explicitly with MAKTABA_COOKIES_SECURE.
			p9 := router.MountP9(r, router.P9Deps{
				DB:            appDB,
				Keys:          auth.keys,
				SecureCookies: cookiesSecure(),
				AccessTTL:     accessTokenTTL(),
			})
			if p9 != nil {
				cookieAuth = p9.CookieAuth
				csrf = p9.CSRF
				logger.Info("p9: auth handlers mounted", "event", "p9_mounted")
			}

			// Phase 10 — subscriptions, pairing, security disclosure, perf
			// admin. All four packages had a working Mount() but no caller
			// (specs/FULL_IMPLEMENTATION_AUDIT.md §A.4).
			router.MountP10(r, router.P10Deps{
				DB:           appDB,
				Logger:       logger,
				PerfRegistry: perfRegistry,
			})
			logger.Info("p10: subscriptions/discovery/security/perf handlers mounted",
				"event", "p10_mounted")
		}
	} else {
		logger.Info("p6: DATABASE_URL unset; handlers unwired",
			"event", "p6_unwired")
	}

	// Forward /healthz on the public port too — convenient for compose
	// stacks that haven't been told about the admin port. Liveness is
	// cheap; exposing it twice costs nothing.
	publicMux := http.NewServeMux()
	publicMux.Handle("/healthz", health.NewLive("api"))
	publicMux.Handle("/api/.well-known/jwks.json", &keys.JWKSHandler{Set: auth.keys})
	publicMux.Handle("/", r)

	publicSrv := &http.Server{
		Addr:              publicAddr,
		Handler:           auth.applySecurity(publicMux, cookieAuth, csrf),
		ReadHeaderTimeout: 10 * time.Second,
	}
	adminSrv := &http.Server{
		Addr:              adminAddr,
		Handler:           adminMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 2)
	go func() {
		logger.Info("listening", "addr", publicAddr, "kind", "public", "event", "http_listen")
		errCh <- publicSrv.ListenAndServe()
	}()
	go func() {
		logger.Info("listening", "addr", adminAddr, "kind", "admin", "event", "http_listen")
		errCh <- adminSrv.ListenAndServe()
	}()

	// Reaper for the rotation overlap window (Story 10.6 AC-4): once
	// `rotation_overlap_sec` has elapsed since the previous-key was
	// added, drop it. 60s tick is much finer than the 24h overlap so
	// the reaper is essentially free.
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				if reaped := auth.keys.ReapExpired(now); reaped {
					logger.Info("auth: reaped previous JWT key after overlap",
						"event", "auth_keys_reaped")
				}
			}
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down", "event", "http_shutdown")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace())
		defer cancel()
		errPublic := publicSrv.Shutdown(shutdownCtx)
		errAdmin := adminSrv.Shutdown(shutdownCtx)
		_ = traceShutdown(shutdownCtx)
		// EC: a request that exceeds the grace is forcibly dropped via
		// Close so the listener doesn't linger past the budget.
		_ = publicSrv.Close()
		_ = adminSrv.Close()
		return errors.Join(errPublic, errAdmin)
	case err := <-errCh:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = publicSrv.Shutdown(shutdownCtx)
		_ = adminSrv.Shutdown(shutdownCtx)
		_ = traceShutdown(shutdownCtx)
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

// idempotencyTTL is how long a replay record is honoured. Story 7.1's
// TTL-bounded growth contract; HLB-315 keeps the same 24h window for
// the Postgres-backed store so behaviour is unchanged from the
// in-memory era.
const idempotencyTTL = 24 * time.Hour

// buildIdempotencyStore returns the idempotency Store, a blocking
// sweep loop the caller runs in a goroutine, and a cleanup func the
// caller defers so the dedicated pool is released on shutdown. When
// DATABASE_URL is set the store is Postgres-backed (durable across
// restarts, shared across replicas — the whole point of an idempotency
// key); otherwise it falls back to the in-memory store so dev / no-DB
// stubs still work.
//
// The Postgres store opens a dedicated small pool: the replay path is
// latency-sensitive and must not contend with the handler or
// readiness pools (same isolation rationale as the P6 wiring). That
// pool is closed by the returned cleanup func on graceful shutdown so
// it doesn't leak; for the in-memory fallback the cleanup is a no-op.
func buildIdempotencyStore(logger *slog.Logger) (idempotency.Store, func(), func()) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		s := idempotency.NewMemoryStore()
		logger.Info("idempotency: in-memory store (DATABASE_URL unset; lost on restart)",
			"event", "idempotency_memory")
		return s, func() { runIdempotencySweep(s, logger) }, func() {}
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		s := idempotency.NewMemoryStore()
		logger.Warn("idempotency: sql.Open failed; falling back to in-memory store",
			"err", err, "event", "idempotency_db_open_failed")
		return s, func() { runIdempotencySweep(s, logger) }, func() {}
	}
	db.SetMaxOpenConns(envIntDefault("MAKTABA_IDEM_DB_MAX_OPEN", 4))
	db.SetMaxIdleConns(envIntDefault("MAKTABA_IDEM_DB_MAX_IDLE", 2))
	s := idempotency.NewPostgresStoreDB(db, logger)
	logger.Info("idempotency: Postgres-backed store (survives restart, replica-safe)",
		"event", "idempotency_postgres")
	return s, func() { runIdempotencySweep(s, logger) }, func() {
		if cerr := db.Close(); cerr != nil {
			logger.Warn("idempotency: closing dedicated pool failed",
				"err", cerr, "event", "idempotency_db_close_failed")
		}
	}
}

// runIdempotencySweep deletes idempotency-key entries older than the
// TTL every 5 min. Works against any Store backend (in-memory map or
// the Postgres `idempotency_keys` table); the SweepExpired contract is
// the same. This is the documented TTL-bounded growth reaper — no
// external scheduled sweeper is required.
//
// A per-tick SweepExpired failure (lock contention, perms) is logged
// at Warn — mirroring the auth-key reaper's event-keyed logging — so a
// persistently failing reaper is visible before disk pressure, rather
// than silently discarded. The loop keeps running on error so a
// transient failure self-heals on the next tick.
func runIdempotencySweep(s idempotency.Store, logger *slog.Logger) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		if _, err := s.SweepExpired(context.Background(), idempotencyTTL); err != nil {
			logger.Warn("idempotency: sweep failed; entries will accrue until it recovers",
				"err", err, "event", "idempotency_sweep_failed")
		}
	}
}

// shutdownGrace honours $MAKTABA_SHUTDOWN_GRACE (e.g. "30s"); falls
// back to defaultShutdownGrace when unset or unparseable.
func shutdownGrace() time.Duration {
	v := os.Getenv("MAKTABA_SHUTDOWN_GRACE")
	if v == "" {
		return defaultShutdownGrace
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return defaultShutdownGrace
	}
	return d
}

func envIntDefault(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// cookiesSecure reports whether the Set-Cookie response should include
// the Secure attribute. Off by default (dev / non-TLS); set
// MAKTABA_COOKIES_SECURE=1 in production OR enable MAKTABA_HSTS=1 (the
// latter is the canonical "we're behind TLS" toggle).
func cookiesSecure() bool {
	if v := os.Getenv("MAKTABA_COOKIES_SECURE"); v == "1" || strings.EqualFold(v, "true") {
		return true
	}
	if v := os.Getenv("MAKTABA_HSTS"); v == "1" || strings.EqualFold(v, "true") {
		return true
	}
	return false
}

// accessTokenTTL honours MAKTABA_AUTH_ACCESS_TTL_SEC; falls back to the
// canonical 15 min default when unset.
func accessTokenTTL() time.Duration {
	v := os.Getenv("MAKTABA_AUTH_ACCESS_TTL_SEC")
	if v == "" {
		return 15 * time.Minute
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 15 * time.Minute
	}
	return time.Duration(n) * time.Second
}

// buildChecks assembles the readiness check set from the runtime
// environment. The list is deliberately bounded — every check costs a
// real round-trip every time the orchestrator polls, so we keep it to
// the dependencies that, if absent, mean the API genuinely cannot
// serve traffic.
func buildChecks(logger *slog.Logger) []health.Check {
	checks := []health.Check{}

	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			logger.Warn("readiness: skipping db check, sql.Open failed",
				"err", err, "event", "readiness_check_skipped")
		} else {
			db.SetMaxOpenConns(1)
			checks = append(checks, &health.DBPing{DB: db})
		}
	}

	if peers := os.Getenv("MAKTABA_GRPC_PEERS"); peers != "" {
		for _, pair := range strings.Split(peers, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			name, addr, ok := strings.Cut(pair, "=")
			if !ok {
				addr = pair
				name = "peer"
			}
			checks = append(checks, &health.TCPDial{N: strings.TrimSpace(name), Addr: strings.TrimSpace(addr)})
		}
	}

	return checks
}

func buildAggregatorServices() []system.Service {
	raw := os.Getenv("MAKTABA_HEALTH_PEERS")
	if raw == "" {
		return nil
	}
	out := []system.Service{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, url, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		out = append(out, system.Service{
			Name: strings.TrimSpace(name),
			URL:  strings.TrimSpace(url),
		})
	}
	return out
}

func warmPeriod() time.Duration {
	v := os.Getenv("MAKTABA_HEALTH_WARM")
	if v == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 30 * time.Second
	}
	return d
}
