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
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/authz"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/pat"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/link"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/schemaguard"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
	grpcpipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
	grpcstreaming "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/streaming"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/logs"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/processes"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/watch"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/nodemode"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/perf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/sharepath"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/system"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvremote"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/updates"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
	errrpt "github.com/Hamza-Labs-Core/Maktaba/shared/errrpt/go"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	health "github.com/Hamza-Labs-Core/Maktaba/shared/health/go"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
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
				if errors.Is(err, errSchemaAhead) {
					os.Exit(exitSchemaAhead)
				}
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
				if errors.Is(err, errUserExists) {
					os.Exit(3)
				}
				os.Exit(1)
			}
			return
		case "hash-password":
			if err := runHashPassword(os.Stdin, os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "hash-password: %v\n", err)
				os.Exit(1)
			}
			return
		case "plex-link":
			if err := runPlexLink(os.Args[2:], os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "plex-link: %v\n", err)
				os.Exit(1)
			}
			return
		case "keys":
			if err := runKeys(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "keys: %v\n", err)
				os.Exit(1)
			}
			return
		case "cloudflare":
			if err := runCloudflare(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "cloudflare: %v\n", err)
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
		// In-memory ring sink for the diagnostics export. Operators can
		// size it via $MAKTABA_LOG_RING_CAPACITY; 0/unset uses the
		// package default (10k lines).
		RingCapacity: envIntDefault("MAKTABA_LOG_RING_CAPACITY", 0),
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
  hash-password
             Hash a password (stdin) for adduser --password-hash-file
             (the desktop app's first-run setup)
  plex-link  Store the Plex server an account token reaches (the desktop
             app's first-run setup; see "plex-link --help")
  keys       Generate or rotate RS256 JWT keys (Story 10.6)
  cloudflare spike
             Run the Cloudflare Stream spike against a real account
             (credentials from the environment; tools/tv/cf-spike.md)
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
	processStart := time.Now()

	// Epic 35 story 35.8: a full node (MAKTABA_NODE_MODE=1) never migrates and needs
	// its home's address; checked before anything binds.
	nodeCfg := nodemode.FromEnv(os.Getenv)
	if err := checkNodeEnv(nodeCfg); err != nil {
		return err
	}
	// lib/pq has no sslmode=prefer, which the home hands every joined node: every
	// DATABASE_URL pool below opens with pgdsn.Open, which settles it per connection,
	// and the event bus's listener with pgdsn.ForLibPQ.
	boundNodeDatabaseURL(nodeCfg)

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
	// Epic 38 story 38.12: never serve on a schema a newer Maktaba migrated (a
	// node's database is the home's, which its schema guard judges)
	if !nodeCfg.Enabled {
		if err := checkSchemaNotAhead(logger, os.Getenv("DATABASE_URL"), knownSchemaRev()); err != nil {
			return err
		}
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

	// /metrics on admin port, no bearer required because the admin port
	// is already firewalled in production deploys. It carries this
	// machine's supervisor's update metrics too (story 35.15): one
	// scrape target per machine.
	adminMux.Handle("/metrics", metricsHandler(os.Getenv("MAKTABA_SUPERVISOR_URL")))

	// Recent-logs endpoint on the admin port, mirroring the streaming
	// service. The public-facing viewer/export use /api/admin/logs/*
	// (auth-gated); this internal one is for symmetric cross-service
	// proxying and is bound to the firewalled admin port.
	adminMux.Handle("/logs/recent", mlog.RecentHandler(mlog.Ring()))

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
	idemStore, idemSweep, idemClose := buildIdempotencyStore(logger, nodeCfg.Enabled)
	go idemSweep()
	defer idemClose()

	// Health-aggregator stats: a dedicated small pool for the live
	// queue-depth count (isolated from the handler + readiness pools,
	// same rationale as P6) and the data volume for the free-disk stat.
	statsDataDir, statsDB, statsClose := buildAggregatorStats(logger)
	defer statsClose()

	r := router.New(router.Deps{
		IdempotencyStore:   idemStore,
		IPRatePerMin:       envIntDefault("MAKTABA_IP_RATE_PER_MIN", 6000),
		UserRatePerMin:     envIntDefault("MAKTABA_USER_RATE_PER_MIN", 600),
		SchemaRev:          envIntDefault("MAKTABA_SCHEMA_REV", schemaguard.SchemaRev),
		AggregatorServices: buildAggregatorServices(),
		StatsDataDir:       statsDataDir,
		StatsDB:            statsDB,
		ErrorReporter:      errReporter,
	})

	// Diagnostics / log-collection surface (/api/admin/logs/* +
	// /api/diagnostics/export). Mounted independent of the P6 app-DB
	// block: it works off the in-memory ring buffer even with no DB, and
	// reuses the dedicated stats pool for the connection/job sections so
	// it never contends with handler traffic. Peer log URLs let the
	// bundle proxy the streaming + pipeline ring buffers.
	hubRef := &router.HubRef{} // Epic 35: the update driver's cluster channel (and a machine's log files)
	router.MountLogs(r, router.LogsDeps{
		DB:                 statsDB,
		DataDir:            statsDataDir,
		SchemaRev:          envIntDefault("MAKTABA_SCHEMA_REV", schemaguard.SchemaRev),
		StartTime:          processStart,
		AggregatorServices: buildAggregatorServices(),
		StatsDB:            statsDB,
		Peers:              buildLogPeers(),
		// this machine's server.log, app.log and launcher.log from its supervisor, and a
		// machine's over the machine channel: where an update's story is
		Files: router.LogFiles(os.Getenv("MAKTABA_SUPERVISOR_URL"), os.Getenv("MAKTABA_SUPERVISOR_TOKEN_FILE"), hubRef, nil),
	})
	logger.Info("logs: diagnostics export surface mounted", "event", "logs_mounted")

	// Process supervision (Story 22.9): the supervisor's per-process
	// state at /api/system/processes, and its crash/hang events copied
	// into health_samples for the control centre's health history.
	supervisorURL := os.Getenv("MAKTABA_SUPERVISOR_URL")
	router.MountProcesses(r, router.ProcessesDeps{SupervisorURL: supervisorURL})
	if recorder := processRecorder(supervisorURL, statsDB, nodeCfg.Enabled, logger); recorder != nil {
		go recorder.Run(context.Background())
	}

	// Epic 28 (Auto-Update) — the update-check service (Story 28.2) and
	// the admin self-update endpoint (Story 28.3). Mounted independent of
	// the app-DB block: it needs no DB, only the build version + network.
	// The background poller runs for the process lifetime; the admin
	// route's auth rides the principal middleware applySecurity installs.
	// A full node checks nothing itself: the home updates the cluster (these routes
	// are home class there, proxied).
	updatesRef := &router.UpdatesRef{} // Epic 35: filled once the app database is open
	var updater *system.Updater
	if !nodeCfg.Enabled {
		updater = buildUpdater(logger)
		go updater.Run(context.Background())
		router.MountP28(r, router.P28Deps{Updater: updater, Updates: updatesRef})
		logger.Info("p28: auto-update surface mounted", "event", "p28_mounted")
	}

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
	// patAuth is the personal-access-token credential middleware
	// (web-pages-batch2). Built once a DB is available and installed in
	// the security chain alongside JWT/cookie. Nil when the auth surface
	// is unwired (no DB).
	var patAuth func(http.Handler) http.Handler
	// nodeRT is a full node's runtime (nil on the home); nodeVerifier checks the
	// nodes' signed requests on the home (Epic 35 stories 35.8-35.10).
	var nodeRT *nodeRuntime
	var nodeFatal <-chan error // nil on the home: never ready
	var nodeVerifier *delegation.Verifier
	// homeLinkSrv is the home's cluster link (story 35.16): its port then serves the
	// machines' mutual TLS beside plain HTTP (nil: plain HTTP only)
	var homeLinkSrv *homeLink
	if nodeCfg.Enabled {
		rt, closeNode, err := mountNode(r, nodeCfg, statsDataDir, logger)
		if err != nil {
			return err
		}
		defer closeNode()
		nodeRT = rt
		nodeFatal = rt.fatal
	} else if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		appDB, dbErr := pgdsn.Open(dsn)
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

			// Story 31.3: canonical share paths (smb://nas/share/...) resolve
			// through the home's mounts, as the pipeline reports them.
			sharepath.Start(context.Background(), appDB, logger)

			// MAKTABA_PIPELINE_ADDR is the API's dial address for the
			// pipeline; MAKTABA_PIPELINE_GRPC_ADDR (the address the
			// pipeline itself listens on) is accepted as a fallback so a
			// shared env file wires both sides.
			pipelineAddr := os.Getenv("MAKTABA_PIPELINE_ADDR")
			if pipelineAddr == "" {
				pipelineAddr = os.Getenv("MAKTABA_PIPELINE_GRPC_ADDR")
			}
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
			// MAKTABA_PIPELINE_SEARCH_ADDR is the pipeline worker that holds the
			// search index (the supervisor's index worker): the semantic search
			// goes there, the rest to pipelineAddr.
			var searchPipelineClient grpcpipeline.Client
			if searchAddr := os.Getenv("MAKTABA_PIPELINE_SEARCH_ADDR"); searchAddr != "" {
				cfg := grpcpipeline.DefaultConfig()
				cfg.Addr = searchAddr
				searchPipelineClient = grpcpipeline.NewRealClient(cfg)
				logger.Info("p6: pipeline search gRPC client wired",
					"addr", searchAddr, "event", "p6_pipeline_search_wired")
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

			// The semantic leg's per-request deadline (embed + Chroma
			// top-k + snippet load); search.DefaultSemanticBudget when unset.
			var semanticBudget time.Duration
			if v := os.Getenv("MAKTABA_SEARCH_SEMANTIC_BUDGET_MS"); v != "" {
				if ms, perr := strconv.Atoi(v); perr == nil && ms > 0 {
					semanticBudget = time.Duration(ms) * time.Millisecond
				} else {
					logger.Warn("p6: MAKTABA_SEARCH_SEMANTIC_BUDGET_MS ignored", "value", v)
				}
			}

			// PipelineClient also carries semantic search: MountP6 derives
			// the search handler's SemanticClient from it (FTS-only when
			// the pipeline address is unset or the pipeline has no index).
			// Epic 32 story 32.6: the home's sealed secrets (slot 0120;
			// key file MAKTABA_SECRETS_KEY, default <config dir>/secrets.key)
			// and the one-time move of extension secrets (the Plex token)
			// out of extensions.settings, which machines can read.
			homeSecrets := router.OpenHomeSecrets(context.Background(), appDB,
				secrets.KeyPath(os.Getenv), logger)
			// one notification hub for every producer (story 33.13): the bell
			// reads its table; e-mail / push subscribe to it later
			notifyHub := notify.NewHub(appDB, logger)
			var mountRemote func(r chi.Router)
			var remoteCtl *tvremote.Controller
			// BusDSN: the event bus's pq.Listener takes a connection string (lib/pq
			// has no connector-based listener), so its sslmode is settled once, here
			// at startup, not per connection as appDB's is.
			// one runtime settings reader for the settings and auth surfaces: a
			// PATCH /api/settings (a renamed home) reads at once in /api/auth/me
			runtimeSettings := runtimecfg.New(appDB)
			router.MountP6(r, router.P6Deps{
				Settings: runtimeSettings,
				Secrets:  homeSecrets,
				OnQueues: func(q *queues.Handler) {
					_, remoteCtl, mountRemote = router.WireP32Remote(q, router.P32RemoteDeps{
						DB: appDB, Secrets: homeSecrets, Logger: logger, Notifier: notifyHub,
					})
				},
				DB:                   appDB,
				PipelineClient:       pipelineClient,
				SearchPipelineClient: searchPipelineClient,
				StreamingClient:      streamingClient,
				SearchSemanticBudget: semanticBudget,
				BusCtx:               busCtx,
				BusDSN:               pgdsn.ForLibPQ(context.Background(), dsn, pgdsn.Ping),
				BusResettle:          func(ctx context.Context) string { return pgdsn.ForLibPQ(ctx, dsn, pgdsn.Ping) },
				EnrichAutoAcceptCtx:  busCtx,
				PerfRegistry:         perfRegistry,
				URLSigner:            streamURLSigner(auth.keys, logger),
			})
			logger.Info("p6: handlers mounted", "event", "p6_mounted")

			// Epic 16 — build the entitlement store ONCE and share the
			// single instance with both the auth surface (Story 16.2
			// seat enforcement on POST /api/users) and the
			// subscriptions surface (apply/revoke). Separate stores
			// would let a license apply be invisible to the seat gate.
			p10Deps := router.P10Deps{
				DB:           appDB,
				Keys:         auth.keys,
				Logger:       logger,
				PerfRegistry: perfRegistry,
			}
			entStore, entVerifier := router.EntitlementStore(p10Deps)
			p10Deps.SubscriptionsStore = entStore

			// Phase 9 (Epic 10 stories 10.2-10.5, 10.16) — auth surface.
			// Reuses the same app DB. SecureCookies tracks MAKTABA_HSTS
			// as a reasonable proxy for "we're behind TLS"; operators
			// can opt in/out explicitly with MAKTABA_COOKIES_SECURE.
			p9 := router.MountP9(r, router.P9Deps{
				DB:            appDB,
				Keys:          auth.keys,
				SecureCookies: cookiesSecure(),
				AccessTTL:     accessTokenTTL(),
				Seats:         router.SeatGate(entStore, entVerifier),
				Settings:      runtimeSettings,
				// Epic 42 story 42.7: the relay agent's identity (the
				// supervisor passes its path while [cloud] is on)
				CloudIdentityPath: os.Getenv("MAKTABA_CLOUD_IDENTITY"),
			})
			if p9 != nil {
				// the cookie principal, then the cloud link gate over every
				// principal (JWT, PAT and cookie alike): a cloud sign-in
				// whose link is gone is no one, one lowered to play is play
				gate := p9.Cloud.Gate.Middleware
				cookie := p9.CookieAuth
				cookieAuth = func(next http.Handler) http.Handler { return cookie(gate(next)) }
				if agent := os.Getenv("MAKTABA_CLOUDLINK_ADMIN"); agent != "" {
					rep := cloudlink.NewReporter(agent, p9.Cloud.Store, logger)
					p9.Cloud.Store.Changed = rep.Kick
					go rep.Run(context.Background(), 30*time.Minute)
				}
				csrf = p9.CSRF
				logger.Info("p9: auth handlers mounted", "event", "p9_mounted")
			}

			// Phase 10 — subscriptions, pairing, security disclosure, perf
			// admin. All four packages had a working Mount() but no caller
			// (specs/FULL_IMPLEMENTATION_AUDIT.md §A.4).
			pairingStore := router.MountP10(r, p10Deps)
			logger.Info("p10: subscriptions/discovery/security/perf handlers mounted",
				"event", "p10_mounted")

			// web-pages-batch2 — admin Library-ACL + self-service PAT
			// route handlers, plus the PAT credential middleware wired
			// into the security chain below.
			router.MountP11(r, router.P11Deps{DB: appDB})
			patAuth = pat.Middleware(pat.New(appDB), users.New(appDB), &authz.ACLStore{DB: appDB})
			logger.Info("p11: library-acl + api-token handlers mounted",
				"event", "p11_mounted")

			// Epic 27 (Live Channels) — channel definition CRUD (27.1)
			// and the EPG / guide + XMLTV/M3U read surface (27.4).
			router.MountP27(r, router.P27Deps{
				DB:            appDB,
				PublicBaseURL: os.Getenv("MAKTABA_PUBLIC_BASE_URL"),
				DataDir:       statsDataDir,
				Secrets:       homeSecrets,
				RemoteInfo:    router.RemoteAvailability(remoteCtl),
			})
			if mountRemote != nil {
				mountRemote(r) // Epic 32 story 32.6: /api/tv/remote
			}
			// The System health page: every service, worker, dependency,
			// integration, queue and TV part, ok / degraded / down (admin).
			// The public /api/system/health liveness probe is unchanged.
			router.MountStatus(r, router.StatusDeps{
				DB: appDB, Version: version.Version, StartTime: processStart,
				PipelineAddr: pipelineAddr, SupervisorURL: supervisorURL, Secrets: homeSecrets,
			})
			// Epic 33: TV changes (/api/tv/changes, releases, slots) and the change
			// controller (staged -> verified -> approved -> swapped)
			router.MountP33(r, router.P33Deps{DB: appDB, Logger: logger, RunController: true,
				Notifier: notifyHub})
			// Epic 35 story 35.3: update policy, release catalog and the quiet-window
			// scheduler (/api/cluster/updates). Verification is story 35.1's.
			router.MountP35(r, router.P35Deps{DB: appDB, Logger: logger, Current: version.Version, Notifier: notifyHub,
				BaseChannel: version.Channel(), Ref: updatesRef, RunLoops: true, Hub: hubRef, GitHubBaseURL: e2eGitHubAPI(),
				Verifier: updates.ReleaseVerifier{Trust: updater}, DataDir: statsDataDir})
			logger.Info("p27: live-channel + guide handlers mounted",
				"event", "p27_mounted")

			// Epic 29 (Watch Analytics) — session lifecycle + history +
			// activity (29.1/29.2/29.4), admin dashboard + per-video
			// stats + export (29.3/29.5/29.6). The session reaper is a
			// boot goroutine (interrupted-session reap every minute +
			// daily retention purge), mirroring runPairingSweep.
			router.MountP29(r, router.P29Deps{DB: appDB, Driver: "postgres"})
			go (&watch.Reaper{
				DB:            appDB,
				RetentionDays: envIntDefault("MAKTABA_ANALYTICS_RETENTION_DAYS", 365),
				Logger:        logger,
			}).Run(context.Background())
			logger.Info("p29: watch-analytics handlers mounted + reaper started",
				"event", "p29_mounted")

			// Epic 31 (multi-instance) — instance identity, the LAN
			// announcement (MAKTABA_DISCOVERY_MDNS=0 turns it off), the
			// machine join/approve/revoke surface and its housekeeping.
			clusterClose, verifier, hl := mountCluster(r, appDB, dsn, publicAddr, homeSecrets, hubRef, updatesRef, notifyHub, logger)
			defer clusterClose()
			nodeVerifier = verifier
			homeLinkSrv = hl
			// Epic 35 stories 35.9, 35.11: the nodes' delegation and cluster sign-in,
			// and this machine's /api/cluster/self
			mountHomeClusterAuth(r, appDB, hubRef, logger)

			// Story 15.6 pairing reaper. Mirrors the idempotency sweeper
			// (go idemSweep()): a boot goroutine expires stale tickets and
			// hard-deletes them past the 7-day retention horizon so
			// `pairing_tickets` cannot grow unbounded. Same context/log
			// convention as runIdempotencySweep.
			go runPairingSweep(pairingStore, logger)
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

	// The home verifies the nodes' signed requests before anything else (story
	// 35.9); a full node serves every route by its placement class (story 35.10).
	publicHandler := delegation.Accept(nodeVerifier, "mkt_sess")(
		auth.applySecurity(publicMux, cookieAuth, csrf, patAuth))
	if nodeRT != nil {
		publicHandler = nodeRT.handler(auth, r, publicMux)
	}
	if homeLinkSrv != nil {
		// a machine's requests on its link go through the same checks as any other
		homeLinkSrv.srv.Handler = publicHandler
	}
	publicSrv := &http.Server{
		Addr:              publicAddr,
		Handler:           publicHandler,
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
		if homeLinkSrv == nil {
			errCh <- publicSrv.ListenAndServe()
			return
		}
		// one port, two kinds of client: the first byte of each connection decides
		// between the machines' mutual TLS and everyone's plain HTTP (story 35.16)
		ln, err := net.Listen("tcp", publicAddr)
		if err != nil {
			errCh <- err
			return
		}
		errCh <- publicSrv.Serve(link.NewSniffListener(ln, homeLinkSrv.srv.TLSConfig()))
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
	case err := <-nodeFatal:
		// a full node started with the home's database down found its role able to
		// run DDL once the database answered: it stops, as it would have at start
		logger.Error("node: stopping", "err", err, "event", "node_role_refused")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = publicSrv.Shutdown(shutdownCtx)
		_ = adminSrv.Shutdown(shutdownCtx)
		_ = traceShutdown(shutdownCtx)
		return err
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
//
// A full node (node) keeps its own in memory: its database role is denied
// idempotency_keys (slot 0103), so the Postgres store only failed every
// write and every sweep.
func buildIdempotencyStore(logger *slog.Logger, node bool) (idempotency.Store, func(), func()) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || node {
		s := idempotency.NewMemoryStore()
		why := "DATABASE_URL unset"
		if node {
			why = "full node: idempotency_keys is the home's"
		}
		logger.Info("idempotency: in-memory store (lost on restart)",
			"reason", why, "event", "idempotency_memory")
		return s, func() { runIdempotencySweep(s, logger) }, func() {}
	}
	db, err := pgdsn.Open(dsn)
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

// pairingSweepInterval is the Story 15.6 reaper cadence ("30 s
// sweeper"). pairingRetention is the hard-delete horizon: a ticket is
// already treated as expired by Get/Consume the instant it passes its
// ExpiresAt, but the row is retained for 7 days afterwards (Story 15.6
// "7 d hard delete") so a just-missed pairing can still surface a
// precise "expired" status before the row is reaped.
const (
	pairingSweepInterval = 30 * time.Second
	pairingRetention     = 7 * 24 * time.Hour
)

// runPairingSweep hard-deletes pairing tickets whose expiry is older
// than the 7-day retention horizon, every 30 s. Mirrors
// runIdempotencySweep exactly (same ticker-driven loop, same
// context.Background(), same event-keyed Warn-on-error that keeps the
// loop alive so a transient failure self-heals next tick). Works
// against any PairingStore backend — the Postgres `pairing_tickets`
// table or the in-memory dev store — via the Sweep seam.
func runPairingSweep(s discovery.PairingStore, logger *slog.Logger) {
	t := time.NewTicker(pairingSweepInterval)
	defer t.Stop()
	for range t.C {
		before := time.Now().UTC().Add(-pairingRetention)
		if _, err := s.Sweep(context.Background(), before); err != nil {
			logger.Warn("pairing: sweep failed; expired tickets will accrue until it recovers",
				"err", err, "event", "pairing_sweep_failed")
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

// buildUpdater constructs the Epic 28 update-check service from env. The
// channel defaults to the running build's channel (version.Channel());
// MAKTABA_UPDATE_INTERVAL_SEC tunes the poll cadence (0 disables the
// background loop), MAKTABA_UPDATE_DISABLE is the no-network kill switch,
// MAKTABA_UPDATE_REPO retargets a fork, and MAKTABA_GITHUB_TOKEN raises
// the GitHub rate limit.
func buildUpdater(logger *slog.Logger) *system.Updater {
	interval := time.Duration(envIntDefault("MAKTABA_UPDATE_INTERVAL_SEC",
		int(system.DefaultCheckInterval/time.Second))) * time.Second
	disabled := func() bool {
		v := os.Getenv("MAKTABA_UPDATE_DISABLE")
		return v == "1" || strings.EqualFold(v, "true")
	}()
	// The Sigstore trusted root for release verification (Epic 35 story
	// 35.1): embedded in the binary, refreshed daily through TUF into
	// <data_dir>/sigstore. Expiry fails verification closed; the critical
	// notification for it lands with story 35.13 — until then it is logged.
	dataDir := dataDirFromEnv()
	roots := &releaseverify.RootStore{
		Dir: filepath.Join(dataDir, "sigstore"),
		OnExpired: func(err error) {
			logger.Error("sigstore trusted root expired: release verification fails closed",
				"event", "update_root_expired", "err", err.Error())
		},
	}
	return system.NewUpdater(system.UpdaterConfig{
		Roots:          roots,
		Repo:           os.Getenv("MAKTABA_UPDATE_REPO"),
		CurrentVersion: version.Version,
		Channel:        version.Channel(),
		Interval:       interval,
		Disabled:       disabled,
		Token:          os.Getenv("MAKTABA_GITHUB_TOKEN"),
		Logger:         logger,
	})
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
		db, err := pgdsn.Open(dsn)
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

// dataDirFromEnv is the API's data volume (the Sigstore root it refreshes,
// uploaded channel art, the free space the health page shows):
// MAKTABA_DATA_DIR, which maktaba-server sets to its data_dir, else
// defaultDataDir for this process.
func dataDirFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("MAKTABA_DATA_DIR")); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return defaultDataDir(os.Geteuid(), os.Getenv("XDG_DATA_HOME"), home)
}

// defaultDataDir is /var/lib/maktaba for a system service (root: the
// canonical data volume of the deploy configs, the server.toml, the systemd
// units, the Prometheus disk alert), else the user's own XDG data dir
// ($XDG_DATA_HOME/maktaba, ~/.local/share/maktaba): a user's API cannot
// write /var/lib.
func defaultDataDir(euid int, xdgDataHome, home string) string {
	switch {
	case euid == 0:
		return "/var/lib/maktaba"
	case filepath.IsAbs(xdgDataHome):
		return filepath.Join(xdgDataHome, "maktaba")
	case home != "":
		return filepath.Join(home, ".local", "share", "maktaba")
	}
	return "/var/lib/maktaba"
}

// buildAggregatorStats resolves the inputs for the /api/system/health
// stats block: the data volume whose free space the UI shows and a
// dedicated small Postgres pool for the live queue-depth count. The
// pool is isolated from the handler/readiness pools (same rationale as
// the idempotency + P6 wiring) and returned alongside a cleanup the
// caller defers. When DATABASE_URL is unset the DB is nil and the
// queue-depth stat is simply omitted (zero).
func buildAggregatorStats(logger *slog.Logger) (string, *sql.DB, func()) {
	dataDir := dataDirFromEnv()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return dataDir, nil, func() {}
	}
	db, err := pgdsn.Open(dsn)
	if err != nil {
		logger.Warn("health stats: sql.Open failed; queue depth unwired",
			"err", err, "event", "health_stats_db_open_failed")
		return dataDir, nil, func() {}
	}
	db.SetMaxOpenConns(envIntDefault("MAKTABA_HEALTH_DB_MAX_OPEN", 2))
	db.SetMaxIdleConns(1)
	return dataDir, db, func() {
		if cerr := db.Close(); cerr != nil {
			logger.Warn("health stats: closing pool failed",
				"err", cerr, "event", "health_stats_db_close_failed")
		}
	}
}

// processRecorder copies the supervisor's crash and hang events into
// health_samples (Story 22.9), or is nil: no supervisor or database, or a
// full node (node), whose database role may not write health_samples
// (slot 0119), so every event was dropped with a permission error.
func processRecorder(supervisorURL string, db *sql.DB, node bool, logger *slog.Logger) *processes.Recorder {
	if supervisorURL == "" || db == nil || node {
		return nil
	}
	host, _ := os.Hostname()
	return &processes.Recorder{SupervisorURL: supervisorURL, DB: db, Host: host, Logger: logger}
}

// buildLogPeers resolves the downstream services whose /logs/recent
// endpoints the diagnostics bundle proxies. Configured via
// $MAKTABA_LOG_PEERS as "name=url" pairs (comma-separated), e.g.
// "streaming=http://streaming:9101/logs/recent,pipeline=http://pipeline:9102/logs/recent".
// Unset yields no peers — the bundle then carries this service's logs
// only.
func buildLogPeers() []logs.PeerLog {
	raw := os.Getenv("MAKTABA_LOG_PEERS")
	if raw == "" {
		return nil
	}
	out := []logs.PeerLog{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, url, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		out = append(out, logs.PeerLog{
			Service: strings.TrimSpace(name),
			URL:     strings.TrimSpace(url),
		})
	}
	return out
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
