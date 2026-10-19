package main

// `maktaba-streaming slot --slot blue|green|amber` (Epic 33 story 33.1, design
// §2): the channel engine in its own process, behind the home's front door.
// It listens on loopback only, runs a station only for a channel placed on its
// host while its role (from the front door, tv_slots) allows it, writes its HLS
// under <cache>/tv/<slot>/ and beats tv_slots every 5 s. The supervisor
// (maktaba-server) runs it as the tv-slot role and restarts it with backoff.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	health "github.com/Hamza-Labs-Core/Maktaba/shared/health/go"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/placement"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/procattr"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/slot"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/tvmetrics"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version"
)

func runSlot(args []string) error {
	fs := flag.NewFlagSet("slot", flag.ContinueOnError)
	name := fs.String("slot", os.Getenv("MAKTABA_TV_SLOT"), "the slot instance: blue, green or amber")
	listen := fs.String("listen", "", "public address (default 127.0.0.1:8791/8792/8793)")
	admin := fs.String("admin", "", "admin address for /healthz, /readyz (default 127.0.0.1:9111/9112/9113)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !slot.Valid(*name) {
		return fmt.Errorf("--slot must be blue, green or amber (got %q)", *name)
	}
	if *listen == "" {
		*listen = os.Getenv("MAKTABA_HTTP_ADDR")
	}
	if *listen == "" {
		*listen = slot.DefaultAddr(*name)
	}
	if *admin == "" {
		*admin = os.Getenv("MAKTABA_ADMIN_ADDR")
	}
	if *admin == "" {
		*admin = slot.DefaultAdminAddr(*name)
	}
	logger := initLogger().With("slot", *name)
	if err := procattr.ContainSelf(); err != nil {
		logger.Warn("child processes are not contained; they may outlive a crash",
			"err", err.Error(), "event", "procattr_contain_failed")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("a TV slot needs DATABASE_URL")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if !clusterSchema(ctx, pool, 60*time.Second) || !slotSchema(ctx, pool, 60*time.Second) {
		return errors.New("the database has no TV slot schema yet (slots 0123, 0127)")
	}

	cfg := config.Load()
	layout := cache.New(cfg.Cache.Root)
	_ = layout.EnsureTiers()
	b := newTVBase(logger, cfg, pool, layout)
	publicBase := os.Getenv("MAKTABA_PUBLIC_BASE_URL")
	ident := channel.LocalIdentity(ctx, pool, b.hostname, publicBase)
	state := slot.NewState(*name, os.Getenv("MAKTABA_TV_RELEASE"))
	// the role first: a standby that restarts must not clear the live slot's rows
	readRole(ctx, pool, ident.ID, state)

	chRepo := channel.NewPGRepo(pool, b.hostname)
	chRepo.HostID = ident.ID
	repo := slot.Repo{PGRepo: chRepo, State: state}
	settings := slot.Settings{Settings: b.settings, State: state}
	scfg := b.stationConfig(chRepo)
	scfg.AlignSegments = true // two slots' segments start at the same instants (33.4)
	start := channel.SettingsStarter(scfg, settings, b.encoders)
	engine := newSlotEngine(b, repo, settings, slot.Layout{Root: cfg.Cache.Root, Name: *name}, start, ident)
	engine.Fence = slot.Fence{Store: placement.PGStore{Pool: pool}, Self: ident.ID, State: state}
	engine.HostStatusOff = true
	watchChannels(engine, pool)
	runEngineLoops(engine)
	go placement.Listen(context.Background(), pool, logger, func(ch uuid.UUID) {
		engine.CheckFences(context.Background(), ch)
	})

	srv := &slot.Server{State: state, HostID: ident.ID, BaseURL: "http://" + *listen, Version: version.Version,
		Engine: engine, Pool: pool, Logger: logger,
		Channels:     (&channel.HTTPHandler{Engine: engine}).Mount,
		EncodersUsed: func() int { return channel.DefaultEncoderGate().Active() + channel.DefaultVTGate().Active() }}
	go srv.Run(ctx)

	checks := []health.Check{procWatchdog.ReadyCheck("db"), procWatchdog.ReadyCheck("channel_engine")}
	adminMux := health.AdminMuxWithLiveness("tv-slot", []health.Check{procWatchdog.LiveCheck()}, checks, 0)
	adminMux.Handle("/logs/recent", mlog.RecentHandler(mlog.Ring()))
	adminMux.Handle("/metrics", tvmetrics.Handler())
	go procWatchdog.Run(ctx)

	publicSrv := &http.Server{Addr: *listen, Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	adminSrv := &http.Server{Addr: *admin, Handler: adminMux, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 2)
	go func() {
		logger.Info("tv slot listening", "addr", *listen, "kind", "public", "host_id", ident.ID,
			"event", "tv_slot_started")
		errCh <- publicSrv.ListenAndServe()
	}()
	go func() { errCh <- adminSrv.ListenAndServe() }()

	var runErr error
	select {
	case <-ctx.Done():
		logger.Info("tv slot: shutting down", "event", "tv_slot_stopped")
	case runErr = <-errCh:
		// EC1: the port is taken: exit, the supervisor backs off and retries
		logger.Error("tv slot: listener failed", "err", runErr.Error(), "event", "tv_slot_listen_failed")
	}
	engine.StopAll()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = publicSrv.Shutdown(sctx)
	_ = adminSrv.Shutdown(sctx)
	if runErr != nil && !errors.Is(runErr, http.ErrServerClosed) {
		return runErr
	}
	return nil
}

// slotSchema waits for slot 0127's tv_slot_beat (the API migrates at boot).
func slotSchema(ctx context.Context, pool *pgxpool.Pool, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		if slotSchemaReady(ctx, pool) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
}

func slotSchemaReady(ctx context.Context, pool *pgxpool.Pool) bool {
	var ok bool
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return pool.QueryRow(qctx, `SELECT to_regproc('tv_slot_beat') IS NOT NULL`).Scan(&ok) == nil && ok
}

// readRole adopts the slot's role from tv_slots (written by the front door).
func readRole(ctx context.Context, pool *pgxpool.Pool, host string, state *slot.State) {
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var role string
	var epoch int64
	var release *string
	if pool.QueryRow(qctx, `SELECT role, role_epoch, release_id::text FROM tv_slots WHERE host_id = $1 AND slot = $2`,
		host, state.Name).Scan(&role, &epoch, &release) == nil {
		state.SetRole(role, epoch)
		if release != nil {
			state.SetRelease(*release)
		}
	}
}

// newSlotEngine is the engine of a slot: the slot's repo, output dir and budget.
func newSlotEngine(b *tvBase, repo slot.Repo, settings slot.Settings, layout slot.Layout, start channel.StartFunc,
	ident channel.HostIdentity) *channel.Engine {
	ctx := context.Background()
	out := settings.Output(ctx)
	enc := b.encoders.Resolve(ctx, out.Encoder)
	engine := channel.NewEngine(repo, layout, start, out.EffectiveMaxActive(enc.Name), time.Now)
	engine.Host = b.hostname
	engine.Identity = ident
	engine.Logger = b.logger
	engine.Limits = func(ctx context.Context) (int, time.Duration) {
		o := settings.Output(ctx)
		return cluster.Capacity(o, b.encoders.Resolve(ctx, o.Encoder).Name, nil), o.WarmGrace
	}
	clearCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if n, err := engine.ClearStaleRuntime(clearCtx); err == nil && n > 0 {
		b.logger.Info("channels: cleared stale runtime rows", "rows", n, "event", "channel_runtime_cleared")
	}
	cancel()
	b.logger.Info("tv slot: engine ready", "encoder", enc.Name, "max_active", out.EffectiveMaxActive(enc.Name),
		"keyint", out.KeyintSeconds, "event", "tv_slot_engine")
	return engine
}
