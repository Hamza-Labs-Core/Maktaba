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
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	health "github.com/Hamza-Labs-Core/Maktaba/shared/health/go"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/capability"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/ffmpeg"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/grpcsrv"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/handlers"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/procattr"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slots"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/transcripts"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/tvmetrics"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--version":
			printVersion(os.Stdout, os.Args[2:])
			return
		case "serve":
			if err := runServe(); err != nil {
				fmt.Fprintf(os.Stderr, "serve: %v\n", err)
				os.Exit(1)
			}
			return
		case "slot":
			// Epic 33 story 33.1: a TV slot (the channel engine behind the front door)
			if err := runSlot(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "slot: %v\n", err)
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

	// ffmpeg / yt-dlp children must never outlive this process (Story
	// 22.9): on Windows put it in a kill-on-close job they all inherit;
	// on Linux each child gets Pdeathsig (procattr.Tie).
	if err := procattr.ContainSelf(); err != nil {
		logger.Warn("child processes are not contained; they may outlive a crash",
			"err", err.Error(), "event", "procattr_contain_failed")
	}

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

	// Liveness (Story 22.9): /healthz fails once the watchdog heartbeat
	// stalls (the channel engine lock is wedged), so the supervisor
	// restarts the process; the database only affects /readyz.
	checks := append(buildChecks(), procWatchdog.ReadyCheck("db"), procWatchdog.ReadyCheck("channel_engine"))
	warm := warmPeriod()
	adminMux := health.AdminMuxWithLiveness("streaming", []health.Check{procWatchdog.LiveCheck()}, checks, warm)

	// Recent-logs endpoint on the admin port (already firewalled in
	// production), drained by the API's diagnostics-export proxy.
	adminMux.Handle("/logs/recent", mlog.RecentHandler(mlog.Ring()))
	// Epic 32 story 32.11: Maktaba TV cluster metrics (Prometheus text) on the
	// admin port, next to /healthz.
	adminMux.Handle("/metrics", tvmetrics.Handler())

	// Real read stores. When DATABASE_URL is set the probe backend and
	// transcript streamer are Postgres-backed (the production path);
	// otherwise we fall back to the in-memory FakeBackend so a local
	// `serve` against no DB still answers liveness. The pool is shared
	// across the public and gRPC surfaces and closed on shutdown.
	// Node mode (Epic 32.1): a joined machine streams from its Epic 31 node config;
	// its database credential is the one it got when it joined.
	node, nodeMode, err := cluster.NodeModeFromOS(runtime.GOOS)
	if err != nil {
		// never started as the home on a node config it cannot read: the supervisor
		// starts it again, by when a write of it is over
		return fmt.Errorf("the node config: %w", err)
	}
	if nodeMode && os.Getenv("DATABASE_URL") == "" && node.DatabaseURL != "" {
		_ = os.Setenv("DATABASE_URL", node.DatabaseURL)
	}
	// Epic 35 story 35.8 (design §9.1, R13): on a full node (MAKTABA_FULL_NODE=1, set by
	// its supervisor) the streaming role also serves video on demand — its own sessions,
	// read from the NAS through this machine's mounts — besides the channels the home's
	// front door places here.
	fullNode := nodeMode && fullNodeFromEnv()
	if nodeMode {
		logger.Info("streaming: cluster node mode", "node_id", node.NodeID, "full_node", fullNode, "event", "tv_node_mode")
	}

	// Every background loop runs on bg (a child of the signal context) and is
	// waited for before the pool closes: a loop still holding a connection
	// would block pool.Close and the process would never exit.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	bg := newBackground(ctx)

	backendCtx, backendCancel := context.WithTimeout(ctx, 10*time.Second)
	// a full node starts with the home's database down too (PR #145): its pool connects
	// when the database answers, playback reads this machine's copy of it meanwhile
	probeBackend, transcripts, pool, dbUp, closeStores := buildProbeStores(backendCtx, logger, fullNode)
	backendCancel()
	// deferred first, so it runs last: after the listeners shut down
	defer func() {
		start := time.Now()
		if !bg.Stop(shutdownWait) {
			logger.Warn("background loops did not stop; leaving the database pool open",
				"event", "shutdown_loops_stuck")
			return
		}
		logger.Info("background loops stopped", "took_ms", time.Since(start).Milliseconds(),
			"event", "shutdown_loops_stopped")
		closeStores()
	}()
	if nodeMode {
		// story 31.3: a node's share map is its own cluster map (buildNodeTV)
		pathmap.SetDefault(pathmap.FromEnv())
	} else {
		startShareMap(bg, pool, logger)
	}
	var watch *dbWatch
	// Epic 37: a full node's copy of its home's database
	var copyPool *pgxpool.Pool
	if fullNode {
		copyPool = openCopy(bg, logger)
	}
	if pg, ok := probeBackend.(*probe.PGBackend); ok && fullNode && pool != nil {
		watch = &dbWatch{ping: pool.Ping, logger: logger}
		if !dbUp {
			watch.Failed(errors.New("not answering at start"))
		}
		probeBackend = withCopy(bg, pg, copyPool, logger, node.NodeID, watch)
	}

	// One session store for both surfaces: the gRPC OpenSession the API calls
	// inserts the row the public mux's manifest/segment routes then look up
	// (and the reaper idles). Two stores meant every session's manifest 404'd.
	sessions := session.NewMemoryStore(5 * time.Second)

	var publicHandler http.Handler
	if nodeMode {
		// Epic 32.1: a cluster node serves only the signed channel routes the home's
		// front door forwards (no HDHomeRun, guide or video sessions; no SSDP).
		if pool == nil {
			return errors.New("streaming node: the home's database is unreachable")
		}
		cfg := config.Load()
		layout := cache.New(cfg.Cache.Root)
		_ = layout.EnsureTiers()
		if watch != nil && watch.Down() {
			// the channels need the home's database: they start once it answers
			sw := newSwapHandler(homeDBDownHandler())
			bg.Go(func(ctx context.Context) {
				if watch.wait(ctx) == nil {
					sw.set(buildNodeTV(bg, logger, cfg, pool, layout, node))
				}
			})
			publicHandler = sw
		} else {
			publicHandler = buildNodeTV(bg, logger, cfg, pool, layout, node)
		}
		if fullNode {
			publicHandler = splitFullNode(publicHandler, buildVOD(bg, logger, probeBackend, transcripts, sessions),
				buildOfflineTV(bg, logger, cfg, copyPool))
		}
	} else {
		if publicHandler, err = buildPublicHandler(bg, logger, probeBackend, transcripts, pool, sessions); err != nil {
			return err
		}
	}

	publicSrv := &http.Server{
		Addr: publicAddr,
		// every failed request (status 400+) in the log: the reason a player gave up
		Handler:           httpx.LogFailures(logger)(publicHandler),
		ReadHeaderTimeout: 5 * time.Second,
	}
	adminSrv := &http.Server{
		Addr:              adminAddr,
		Handler:           adminMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	bg.Go(procWatchdog.Run)

	errCh := make(chan error, 3)
	go func() {
		logger.Info("listening", "addr", publicAddr, "kind", "public", "event", "http_listen")
		errCh <- serveOn(publicSrv)
	}()
	go func() {
		logger.Info("listening", "addr", adminAddr, "kind", "admin", "event", "http_listen")
		errCh <- serveOn(adminSrv)
	}()

	// gRPC surface for the API (Story 7.18 / 8.8): JSON-codec service
	// matching the api streaming client's wire convention.
	// A cluster node has no video sessions (Epic 32.1): no gRPC surface.
	var grpcServer *grpc.Server
	var grpcLis net.Listener
	grpcErr := errors.New("node mode")
	if !nodeMode || fullNode {
		grpcServer = grpcsrv.NewGRPCServer(buildGRPCServer(bg, logger, probeBackend, sessions))
		grpcLis, grpcErr = listenTCP("tcp", grpcAddr)
	}
	if nodeMode && !fullNode {
		logger.Info("grpc surface off on a cluster node", "event", "grpc_node_off")
	} else if grpcErr != nil {
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

// listenTCP opens every socket runServe listens on (public, admin, gRPC).
// Production: net.Listen. The tests swap in a guard that refuses any address
// that is not loopback, so `go test` never opens a socket on the LAN (on
// Windows that pops a firewall prompt for every new streaming.test.exe).
var listenTCP = net.Listen

// serveOn binds srv.Addr through listenTCP and serves it (ListenAndServe).
func serveOn(srv *http.Server) error {
	ln, err := listenTCP("tcp", srv.Addr)
	if err != nil {
		return err
	}
	return srv.Serve(ln)
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
func buildGRPCServer(bg *background, logger *slog.Logger, backend probe.Backend, store session.Store) *grpcsrv.Server {
	cfg := config.Load()
	allocator := slots.NewAllocator(slots.AllocatorConfig{
		MaxTranscode: cfg.Transcode.MaxConcurrent,
		QueueDepth:   cfg.Transcode.QueueDepth,
	})
	probeCache := probe.NewCache(backend, 4096)

	srv := grpcsrv.New(probeCache, store, allocator, capability.NewRegistry())
	srv.HWAccelNow = watchHWAccel(bg, logger, cfg).Get
	srv.Logger = logger

	// HLB-328: wire the real ffmpeg-spawning orchestrator so
	// OpenSession actually transcodes. The output dir must match the
	// manifest handler's HLSDir so the player's manifest/segment GETs
	// resolve to what FFmpeg writes.
	layout := cache.New(cfg.Cache.Root)
	if lerr := layout.EnsureTiers(); lerr != nil {
		logger.Warn("cache layout init failed", "err", lerr.Error(), "event", "cache_init_warn")
	}
	// VOD's GPU sessions count with the TV's channels' (vodGate)
	srv.Transcode = ffmpeg.DefaultOrchestrator(vodBinary(cfg), map[ffmpeg.HWAccel]ffmpeg.SessionGate{
		ffmpeg.HWAccelNVENC:        vodNVENC,
		ffmpeg.HWAccelVideoToolbox: vodVT,
	}, logger)
	srv.ResolveDir = layout.HLSDir
	// a transcode keeps every segment until its session closes: none starts
	// with less than the floor free on the cache's disk
	disk := vodDisk{root: layout.Root, minFreeGiB: cfg.Transcode.MinFreeGiB}
	srv.TranscodeRoom = func() error { return transcodeRoom(disk, logger) }
	return srv
}

// vodBinary is the ffmpeg the transcodes and their hwaccel probe run: the
// channels' own (MAKTABA_FFMPEG_BIN, the one the supervisor checked), not
// whichever the PATH finds first.
func vodBinary(cfg config.Config) ffmpeg.Binary {
	return ffmpeg.Binary{FFmpeg: cfg.FFmpeg.Binary, FFprobe: cfg.FFmpeg.ProbeBinary}
}

// hwaccelRetry is how often the transcodes' encoder is detected again while
// it is software although ffmpeg lists a GPU encoder (busy or not ready at
// the last try).
const hwaccelRetry = 5 * time.Minute

// watchHWAccel picks the transcodes' encoder (Story 8.7), narrowed by
// MAKTABA_HWACCEL_PREFER: once at start (within 10 s), then again while a
// listed GPU encoder did not work, logging each verdict.
func watchHWAccel(bg *background, logger *slog.Logger, cfg config.Config) *ffmpeg.Choice {
	d := ffmpeg.Default(vodBinary(cfg))
	d.Prefer = cfg.HWAccel.Prefer
	return ffmpeg.WatchChoice(bg.ctx, d, 10*time.Second, hwaccelRetry, func(hw ffmpeg.HWAccel, err error) {
		if err != nil {
			logger.Warn("hwaccel: software", "err", err.Error(), "event", "hwaccel_software_fallback")
			return
		}
		name := string(hw)
		if name == "" {
			name = "software"
		}
		logger.Info("hwaccel detected", "encoder", name, "event", "hwaccel_selected")
	})
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
func buildPublicHandler(bg *background, logger *slog.Logger, backend probe.Backend, transcripts handlers.TranscriptStreamer, pool *pgxpool.Pool, store session.Store) (http.Handler, error) {
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	jwks := buildVerifierKeys(ctx, bg, logger, cfg)

	layout := cache.New(cfg.Cache.Root)
	if err := layout.EnsureTiers(); err != nil {
		logger.Warn("cache layout init failed", "err", err.Error(), "event", "cache_init_warn")
	}

	allocator := slots.NewAllocator(slots.AllocatorConfig{
		MaxTranscode: cfg.Transcode.MaxConcurrent,
		QueueDepth:   cfg.Transcode.QueueDepth,
	})

	probeCache := probe.NewCache(backend, 4096)

	gc := cache.NewGC(layout, cache.GCConfig{
		MaxBytes: cfg.CacheMaxBytes(), Interval: cfg.GCInterval(),
	})
	bg.Go(func(ctx context.Context) {
		tick(ctx, cfg.GCInterval(), func() {
			if _, _, err := gc.Sweep(); err != nil {
				logger.Warn("gc sweep failed", "err", err.Error(), "event", "cache_gc_failed")
			}
		})
	})

	reaper := session.NewReaper(store, session.ReaperConfig{IdleAfter: vodSessionIdle, ActiveIdleAfter: vodActiveIdle,
		Interval: vodSessionSweep, Logger: logger})
	bg.Go(reaper.Run)
	// the closed sessions' folders, and a previous run's (at start, then with
	// the reaper)
	bg.Go(func(ctx context.Context) {
		disk := vodDisk{root: layout.Root, minFreeGiB: cfg.Transcode.MinFreeGiB}
		purge := func() { purgeVODSessions(ctx, layout, store, disk, time.Now(), logger) }
		purge()
		tick(ctx, vodSessionSweep, purge)
	})

	_ = allocator

	// Epic 27 — live channels (Story 27.3) + HDHomeRun emulation (Story
	// 27.5). Wired only with a real Postgres pool (the schedule lives in
	// channel_programs); the no-DB local `serve` skips them entirely.
	var extraMounts []func(chi.Router)
	if pool != nil {
		var err error
		if extraMounts, err = buildHomeTV(bg, logger, cfg, pool, layout); err != nil {
			return nil, err
		}
	}

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
		ExtraMounts: extraMounts,
	}), nil
}

// buildVerifierKeys is the key set the signed-URL middleware verifies against:
// the machine's stream signing key file (MAKTABA_STREAM_SIGNING_KEY, which the
// API on this machine signs with), else the API's JWKS (MAKTABA_JWKS_URL).
// Neither: every signed route refuses (signed-url-bad-signature), logged.
func buildVerifierKeys(ctx context.Context, bg *background, logger *slog.Logger, cfg config.Config) *auth.JWKSCache {
	if path := cfg.JWT.StreamKeyFile; path != "" {
		c := auth.NewKeyFileCache(bg.ctx, path, 2*time.Second, func(kid string) {
			logger.Info("stream URLs verified with this machine's stream key", "kid", kid, "event", "stream_key_loaded")
		})
		if c.AnyKey() == nil {
			logger.Warn("stream key not readable yet; video playback is refused until it is",
				"path", path, "event", "stream_key_pending")
		}
		return c
	}
	if cfg.JWT.JWKSURL == "" {
		logger.Warn("no stream key and no JWKS URL: every signed stream URL is refused",
			"event", "stream_key_missing")
	}
	jwks, err := auth.NewRetryingJWKSCache(ctx, cfg.JWT.JWKSURL, cfg.JWKSRefreshDuration())
	if err != nil {
		logger.Warn("jwks load failed at boot, will keep retrying", "err", err.Error(), "event", "jwks_boot_warn")
	}
	jwks.StartRefreshLoop(bg.ctx)
	return jwks
}

// buildProbeStores constructs the probe Backend + transcript streamer
// the read path consumes, plus a cleanup func the caller defers. When
// DATABASE_URL is set both are Postgres-backed (sharing one pgx pool);
// otherwise the probe backend is the in-memory FakeBackend and there
// is no transcript streamer (auto.vtt is simply not wired). A pool that
// fails to open/ping is fatal-soft: we log and fall back to the fake so
// liveness still answers, matching the JWKS/hwaccel boot-warn pattern.
// With lazy (a full node, PR #145) a database that does not answer keeps
// the Postgres-backed stores on a pool that connects once it does (up
// false); this machine's copy answers meanwhile.
func buildProbeStores(ctx context.Context, logger *slog.Logger, lazy bool) (probe.Backend, handlers.TranscriptStreamer, *pgxpool.Pool, bool, func()) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logger.Info("probe: DATABASE_URL unset; using in-memory FakeBackend",
			"event", "probe_fake_backend")
		return probe.NewFakeBackend(), nil, nil, false, func() {}
	}
	up := true
	backend, pool, err := probe.ConnectPG(ctx, dsn)
	if err != nil && lazy {
		if pool, err = pgxpool.New(context.Background(), dsn); err == nil {
			logger.Warn("probe: the home's database does not answer; starting on this machine's copy of it",
				"event", "probe_pg_down_at_start")
			backend, up = probe.NewPGBackend(pool), false
		}
	}
	if err != nil {
		logger.Warn("probe: postgres connect failed; falling back to FakeBackend",
			"err", err.Error(), "event", "probe_pg_connect_failed")
		return probe.NewFakeBackend(), nil, nil, false, func() {}
	}
	logger.Info("probe: Postgres-backed media_info reads wired", "event", "probe_pg_backend")
	streamer := transcripts.NewPGStreamer(pool)
	return backend, streamer, pool, up, func() { pool.Close() }
}
