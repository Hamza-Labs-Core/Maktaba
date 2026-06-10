// Package main is the entry point for the Maktaba streaming server.
//
// Stub created by Story 22.1; Story 22.3 added the `serve` subcommand
// so compose has a long-lived process to attach a healthcheck to;
// Story 21.1 wired the structured logger; Story 21.4 split serve into
// a public mux (port 8081, real HLS routes land with Epic 08) and an
// admin mux (port 9101, /healthz + /readyz). Epic 08 (Stories 8.1-8.15)
// replaces the placeholder mux with the chi-based byte-pumping router
// — direct play, HLS/DASH manifests, posters/sprites/thumbs, subtitles,
// chapters, plus the JWKS-validated signed-URL middleware.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"

	health "github.com/Hamza-Labs-Core/Maktaba/shared/health/go"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/grpcsrv"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/handlers"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slots"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/transcripts"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--version":
			fmt.Fprintln(os.Stdout, version.String())
			return
		case "serve":
			if err := runServe(); err != nil {
				fmt.Fprintf(os.Stderr, "serve: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	logger := initLogger()
	logger.Info("starting maktaba-streaming",
		"commit", version.Commit,
		"build_date", version.BuildDate,
		"event", "startup",
	)
	logger.Info("stub server: pass `serve` to launch the placeholder HTTP server")
}

// initLogger configures the global structured logger from environment.
// Idempotent: subsequent calls return the cached instance.
func initLogger() *slog.Logger {
	return mlog.Init(mlog.Options{
		Service: "streaming",
		Env:     env(),
		Version: version.Version,
		// In-memory ring sink so the API's diagnostics export can proxy
		// this service's recent logs via /logs/recent on the admin port.
		RingCapacity: envIntDefault("MAKTABA_LOG_RING_CAPACITY", 0),
	})
}

func env() string {
	if v := os.Getenv("MAKTABA_ENV"); v != "" {
		return v
	}
	return "dev"
}

// envIntDefault reads an integer env var, falling back to def when unset
// or unparseable.
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

// runServe stands up two HTTP servers (mirrors api/main.go):
//
//   - Public on $MAKTABA_HTTP_ADDR (default :8081). Today serves only
//     a placeholder; Epic 08 lands the HLS routes.
//   - Admin on $MAKTABA_ADMIN_ADDR (default :9101) carrying /healthz
//     and /readyz for the orchestrator. Plan §0 isolates the probe
//     port so a hot HLS handler can't starve liveness.
func runServe() error {
	logger := initLogger()

	publicAddr := os.Getenv("MAKTABA_HTTP_ADDR")
	if publicAddr == "" {
		publicAddr = ":8081"
	}
	adminAddr := os.Getenv("MAKTABA_ADMIN_ADDR")
	if adminAddr == "" {
		adminAddr = ":9101"
	}
	grpcAddr := os.Getenv("MAKTABA_STREAMING_GRPC_ADDR")
	if grpcAddr == "" {
		grpcAddr = ":9050"
	}

	checks := buildChecks()
	warm := warmPeriod()
	adminMux := health.AdminMux("streaming", checks, warm)

	// Recent-logs endpoint on the admin port (already firewalled in
	// production), drained by the API's diagnostics-export proxy.
	adminMux.Handle("/logs/recent", mlog.RecentHandler(mlog.Ring()))

	// Real read stores. When DATABASE_URL is set the probe backend and
	// transcript streamer are Postgres-backed (the production path);
	// otherwise we fall back to the in-memory FakeBackend so a local
	// `serve` against no DB still answers liveness. The pool is shared
	// across the public and gRPC surfaces and closed on shutdown.
	backendCtx, backendCancel := context.WithTimeout(context.Background(), 10*time.Second)
	probeBackend, transcripts, closeStores := buildProbeStores(backendCtx, logger)
	backendCancel()
	defer closeStores()

	publicHandler := buildPublicHandler(logger, probeBackend, transcripts)

	publicSrv := &http.Server{
		Addr:              publicAddr,
		Handler:           publicHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	adminSrv := &http.Server{
		Addr:              adminAddr,
		Handler:           adminMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 3)
	go func() {
		logger.Info("listening", "addr", publicAddr, "kind", "public", "event", "http_listen")
		errCh <- publicSrv.ListenAndServe()
	}()
	go func() {
		logger.Info("listening", "addr", adminAddr, "kind", "admin", "event", "http_listen")
		errCh <- adminSrv.ListenAndServe()
	}()

	// gRPC surface for the API (Story 7.18 / 8.8): JSON-codec service
	// matching the api streaming client's wire convention.
	grpcServer := grpcsrv.NewGRPCServer(buildGRPCServer(logger, probeBackend))
	grpcLis, grpcErr := net.Listen("tcp", grpcAddr)
	if grpcErr != nil {
		logger.Warn("grpc listen failed; grpc surface disabled",
			"addr", grpcAddr, "err", grpcErr.Error(), "event", "grpc_listen_failed")
	} else {
		go func() {
			logger.Info("listening", "addr", grpcAddr, "kind", "grpc", "event", "grpc_listen")
			errCh <- grpcServer.Serve(grpcLis)
		}()
	}

	stopGRPC := func() {
		if grpcErr == nil {
			grpcServer.GracefulStop()
		}
	}

	select {
	case <-ctx.Done():
		logger.Info("shutting down", "event", "http_shutdown")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		stopGRPC()
		errPublic := publicSrv.Shutdown(shutdownCtx)
		errAdmin := adminSrv.Shutdown(shutdownCtx)
		return errors.Join(errPublic, errAdmin)
	case err := <-errCh:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopGRPC()
		_ = publicSrv.Shutdown(shutdownCtx)
		_ = adminSrv.Shutdown(shutdownCtx)
		if err != nil && err != http.ErrServerClosed && !errors.Is(err, grpc.ErrServerStopped) {
			return err
		}
		return nil
	}
}

// buildGRPCServer constructs the in-process streaming Server backing
// the gRPC surface. It mirrors buildPublicHandler's in-memory store
// wiring (Postgres-backed stores land with the API's JWKS URL); the
// API drives OpenSession / CloseSession / EvictHashCache /
// GetCapabilities / HealthCheck over this.
//
// The probe backend is injected (Postgres-backed in production, the
// in-memory FakeBackend for a no-DB local `serve`) so OpenSession reads
// real media_info rows. HLB-328 (FFmpeg never spawned) is closed below
// via srv.Transcode.
//
// NOTE (deferred residual, tracked): HLB-334 / HLB-338 — the session
// store is still the in-memory MemoryStore. A Postgres-backed
// session.Store plus the streaming_sessions migration the Streaming
// service would own are a net-new schema-ownership change best landed
// on their own; until then sessions live in-process (lost on restart,
// not shared across replicas), which is acceptable for the v1
// single-replica deploy.
func buildGRPCServer(logger *slog.Logger, backend probe.Backend) *grpcsrv.Server {
	cfg := config.Load()
	store := session.NewMemoryStore(5 * time.Second)
	allocator := slots.NewAllocator(slots.AllocatorConfig{
		MaxTranscode: cfg.Transcode.MaxConcurrent,
		QueueDepth:   cfg.Transcode.QueueDepth,
	})
	probeCache := probe.NewCache(backend, 4096)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hw, err := ffmpeg.Default().Detect(ctx)
	if err != nil {
		logger.Info("hwaccel probe failed; using software",
			"err", err.Error(), "event", "hwaccel_software_fallback")
	}

	srv := grpcsrv.New(probeCache, store, allocator, capability.NewRegistry())
	srv.HWAccel = hw

	// HLB-328: wire the real ffmpeg-spawning orchestrator so
	// OpenSession actually transcodes. The output dir must match the
	// manifest handler's HLSDir so the player's manifest/segment GETs
	// resolve to what FFmpeg writes.
	layout := cache.New(cfg.Cache.Root)
	if lerr := layout.EnsureTiers(); lerr != nil {
		logger.Warn("cache layout init failed", "err", lerr.Error(), "event", "cache_init_warn")
	}
	srv.Transcode = ffmpeg.DefaultOrchestrator(ffmpeg.DefaultBinary())
	srv.ResolveDir = layout.HLSDir
	return srv
}

// buildChecks assembles the readiness check set for streaming. The
// service's only hard dependency in v1 is the API (for token-claim
// verification once Epic 08 lands); we model it via a TCP dial against
// $MAKTABA_API_URL's host. EC2 in the story documents the "all
// streaming replicas down" case from the *aggregator's* perspective —
// at the per-service level we just report whether the API is reachable.
func buildChecks() []health.Check {
	checks := []health.Check{}
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

// warmPeriod is plan TC3's 30 s cold-start window. Override via
// $MAKTABA_HEALTH_WARM (e.g. "0s" in tests).
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

// buildPublicHandler stands up the chi router with all the Epic 08
// byte-pumping routes wired. When MAKTABA_JWKS_URL is empty (e.g. local
// `serve` against no API), the JWKS fetch is skipped and routes that
// require auth will surface signed-URL-bad-signature errors — the
// /healthz endpoint stays open so compose's healthcheck still passes.
//
// The probe backend and transcript streamer are injected: Postgres in
// production, the in-memory FakeBackend / nil streamer for a no-DB
// local `serve`. The session store is still in-memory (see
// buildGRPCServer's note); Postgres-backed sessions are a tracked
// follow-up.
func buildPublicHandler(logger *slog.Logger, backend probe.Backend, transcripts handlers.TranscriptStreamer) http.Handler {
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	jwks, err := auth.NewJWKSCache(ctx, cfg.JWT.JWKSURL, cfg.JWKSRefreshDuration())
	if err != nil {
		logger.Warn("jwks load failed at boot, will keep retrying", "err", err.Error(), "event", "jwks_boot_warn")
	} else if cfg.JWT.JWKSURL != "" {
		jwks.StartRefreshLoop(context.Background())
	}

	layout := cache.New(cfg.Cache.Root)
	if err := layout.EnsureTiers(); err != nil {
		logger.Warn("cache layout init failed", "err", err.Error(), "event", "cache_init_warn")
	}

	store := session.NewMemoryStore(5 * time.Second)
	allocator := slots.NewAllocator(slots.AllocatorConfig{
		MaxTranscode: cfg.Transcode.MaxConcurrent,
		QueueDepth:   cfg.Transcode.QueueDepth,
	})

	probeCache := probe.NewCache(backend, 4096)

	// Hwaccel detection — non-fatal on failure; we fall through to
	// software encoding (Story 8.7 AC-1 fallback path).
	hw, err := ffmpeg.Default().Detect(ctx)
	if err != nil {
		logger.Info("hwaccel probe failed; using software", "err", err.Error(), "event", "hwaccel_software_fallback")
	} else {
		logger.Info("hwaccel detected", "encoder", string(hw), "event", "hwaccel_selected")
	}

	gc := cache.NewGC(layout, cache.GCConfig{
		MaxBytes: cfg.CacheMaxBytes(), Interval: cfg.GCInterval(),
	})
	go func() {
		t := time.NewTicker(cfg.GCInterval())
		defer t.Stop()
		for range t.C {
			if _, _, err := gc.Sweep(); err != nil {
				logger.Warn("gc sweep failed", "err", err.Error(), "event", "cache_gc_failed")
			}
		}
	}()

	reaper := session.NewReaper(store, session.ReaperConfig{IdleAfter: 90 * time.Second, Interval: 30 * time.Second})
	go reaper.Run(context.Background())

	_ = allocator
	_ = hw

	return server.New(server.Deps{
		Cfg:         cfg,
		JWKS:        jwks,
		Probe:       probeCache,
		Profiles:    capability.NewRegistry(),
		Sessions:    store,
		Layout:      layout,
		Files:       handlers.OSFileOpener{},
		Transcripts: transcripts,
		Now:         time.Now,
	})
}

// buildProbeStores constructs the probe Backend + transcript streamer
// the read path consumes, plus a cleanup func the caller defers. When
// DATABASE_URL is set both are Postgres-backed (sharing one pgx pool);
// otherwise the probe backend is the in-memory FakeBackend and there
// is no transcript streamer (auto.vtt is simply not wired). A pool that
// fails to open/ping is fatal-soft: we log and fall back to the fake so
// liveness still answers, matching the JWKS/hwaccel boot-warn pattern.
func buildProbeStores(ctx context.Context, logger *slog.Logger) (probe.Backend, handlers.TranscriptStreamer, func()) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logger.Info("probe: DATABASE_URL unset; using in-memory FakeBackend",
			"event", "probe_fake_backend")
		return probe.NewFakeBackend(), nil, func() {}
	}
	backend, pool, err := probe.ConnectPG(ctx, dsn)
	if err != nil {
		logger.Warn("probe: postgres connect failed; falling back to FakeBackend",
			"err", err.Error(), "event", "probe_pg_connect_failed")
		return probe.NewFakeBackend(), nil, func() {}
	}
	logger.Info("probe: Postgres-backed media_info reads wired", "event", "probe_pg_backend")
	streamer := transcripts.NewPGStreamer(pool)
	return backend, streamer, func() { pool.Close() }
}
