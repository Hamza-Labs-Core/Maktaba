package main

// Maktaba TV wiring (Epic 27 engine + Epic 32 cluster): the home runs the channel
// engine behind the placement service and the front door; a node (Epic 32.1) runs
// only signed channel routes for the stations the home places on it.

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/hdhr"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/placement"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version"
)

// clusterSchema waits (up to wait) for slot 0123's tv_host_beat: the API migrates the
// database at boot, possibly after this process started. Without it the TV runs as
// before Epic 32 (the home alone, no placements).
func clusterSchema(ctx context.Context, pool *pgxpool.Pool, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		var ok bool
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := pool.QueryRow(qctx, `SELECT to_regproc('tv_host_beat') IS NOT NULL`).Scan(&ok)
		cancel()
		if err == nil && ok {
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

// tvBase is what both roles share.
type tvBase struct {
	logger   *slog.Logger
	cfg      config.Config
	pool     *pgxpool.Pool
	layout   *cache.Layout
	hostname string
	settings *channel.PGSettings
	encoders *channel.EncoderResolver
	caps     cluster.Capabilities
	cpu      cpuLoad
	loop     *cluster.HostLoop
}

type cpuLoad interface{ Load() float64 }

func newTVBase(logger *slog.Logger, cfg config.Config, pool *pgxpool.Pool, layout *cache.Layout) *tvBase {
	hostname, _ := os.Hostname()
	b := &tvBase{logger: logger, cfg: cfg, pool: pool, layout: layout, hostname: hostname,
		settings: channel.NewPGSettings(pool, logger),
		encoders: &channel.EncoderResolver{FFmpeg: cfg.FFmpeg.Binary},
		cpu:      cluster.NewCPUSampler()}
	return b
}

// override is the admin's capacity override from the last beat (nil: none).
func (b *tvBase) override() *int {
	if b.loop == nil {
		return nil
	}
	if a, ok := b.loop.Answer(); ok {
		return a.CapacityOverride
	}
	return nil
}

// capacity is this host's channels-on-air cap.
func (b *tvBase) capacity(ctx context.Context) (int, string) {
	out := b.settings.Output(ctx)
	enc := b.encoders.Resolve(ctx, out.Encoder).Name
	return cluster.Capacity(out, enc, b.override()), enc
}

// reportedCapacity is the capacity before the admin's override (streaming_hosts.max_active).
func (b *tvBase) reportedCapacity(ctx context.Context) int {
	out := b.settings.Output(ctx)
	return cluster.Capacity(out, b.encoders.Resolve(ctx, out.Encoder).Name, nil)
}

func (b *tvBase) stationConfig(repo channel.Repo) channel.StationConfig {
	return channel.StationConfig{
		FFmpeg:   b.cfg.FFmpeg.Binary,
		Repo:     repo,
		Prober:   channel.NewFFprobe(b.cfg.FFmpeg.ProbeBinary),
		Resolver: channel.NewYtDlp(os.Getenv("MAKTABA_YTDLP")),
		PathMap:  pathmap.FromEnv(),
		FontFile: channel.FindFont(),
		Logger:   b.logger,
		Gate:     channel.DefaultEncoderGate(),
		VTGate:   channel.DefaultVTGate(),
	}
}

func (b *tvBase) newEngine(repo *channel.PGRepo, start channel.StartFunc, ident channel.HostIdentity) *channel.Engine {
	ctx := context.Background()
	out := b.settings.Output(ctx)
	enc := b.encoders.Resolve(ctx, out.Encoder)
	b.logger.Info("channels: output profile", "encoder", enc.Name, "preference", out.Encoder,
		"vt_software", enc.VTSoftware, "width", out.Width, "height", out.Height, "fps", out.FPS,
		"audio", out.AudioCodec, "nvenc_max", out.NVENCMax, "vt_max", out.VTMax,
		"max_active", out.EffectiveMaxActive(enc.Name), "role", ident.Role, "host_id", ident.ID,
		"event", "channel_profile")
	engine := channel.NewEngine(repo, b.layout, start, out.EffectiveMaxActive(enc.Name), time.Now)
	engine.Host = b.hostname
	engine.Identity = ident
	engine.Logger = b.logger
	engine.Limits = func(ctx context.Context) (int, time.Duration) {
		c, _ := b.capacity(ctx)
		return c, b.settings.Output(ctx).WarmGrace
	}
	engine.Status = func(ctx context.Context) channel.HostStatus {
		return channel.HostStatus{
			Encoder:   b.encoders.Resolve(ctx, b.settings.Output(ctx).Encoder).Name,
			NVENCFree: channel.DefaultEncoderGate().Free(),
			VTFree:    channel.DefaultVTGate().Free(),
		}
	}
	clearCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	if n, err := engine.ClearStaleRuntime(clearCtx); err != nil {
		b.logger.Warn("channels: clearing stale runtime rows failed", "err", err.Error(), "event", "channel_runtime_clear_failed")
	} else if n > 0 {
		b.logger.Info("channels: cleared stale runtime rows", "rows", n, "event", "channel_runtime_cleared")
	}
	cancel()
	return engine
}

// runEngineLoops: reap idle stations, keep channel_runtime current, re-check fences.
func runEngineLoops(engine *channel.Engine) {
	go func() {
		engine.SyncRuntime(context.Background())
		t := time.NewTicker(channel.HeartbeatInterval)
		defer t.Stop()
		for i := 1; ; i++ {
			<-t.C
			if i%3 == 0 {
				engine.ReapIdle(context.Background())
			}
			engine.SyncRuntime(context.Background())
		}
	}()
	if engine.Fence != nil {
		go func() {
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for range t.C {
				engine.CheckFences(context.Background())
			}
		}()
	}
}

// beatInfo is the common part of a heartbeat.
func (b *tvBase) beatInfo(ctx context.Context, role string, engine *channel.Engine, base string, unfit string) cluster.BeatInfo {
	_, enc := b.capacity(ctx)
	out := b.settings.Output(ctx)
	ids := engine.ActiveIDs()
	active := make([]string, 0, len(ids))
	for _, id := range ids {
		active = append(active, id.String())
	}
	gate := channel.DefaultEncoderGate()
	return cluster.BeatInfo{
		Host: b.hostname, Role: role, Version: version.Version, Encoder: enc,
		Encoders: b.caps.Encoders, Capacity: b.reportedCapacity(ctx), Active: active,
		Load: map[string]any{"cpu": b.cpu.Load(), "protocol": cluster.ProtocolVersion,
			"gpu_sessions": gate.Active(), "nvenc_max": out.NVENCMax},
		StreamBaseURL: base, CanPush: b.caps.CanPush, CanLive: b.caps.CanLive, Unfit: unfit,
		NVENCFree: gate.Free(), VTFree: channel.DefaultVTGate().Free(),
	}
}

// buildHomeTV wires the channel engine, HDHomeRun, the guide and — with slot 0123 —
// the placement service, the front door and the home's heartbeat.
func buildHomeTV(logger *slog.Logger, cfg config.Config, pool *pgxpool.Pool, layout *cache.Layout) []func(chi.Router) {
	b := newTVBase(logger, cfg, pool, layout)
	ctx := context.Background()
	publicBase := os.Getenv("MAKTABA_PUBLIC_BASE_URL")
	ident := channel.LocalIdentity(ctx, pool, b.hostname, publicBase)
	chRepo := channel.NewPGRepo(pool, b.hostname)
	chRepo.HostID = ident.ID
	start := channel.SettingsStarter(b.stationConfig(chRepo), b.settings, b.encoders)
	engine := b.newEngine(chRepo, start, ident)
	watchChannels(engine, pool)
	chHandler := &channel.HTTPHandler{Engine: engine}

	var mounts []func(chi.Router)
	var streamer hdhr.Streamer = engine
	if clusterSchema(ctx, pool, 60*time.Second) {
		fd, assets := b.wireHomeCluster(ctx, engine, chHandler, ident, chRepo)
		mounts = append(mounts, fd.Mount, fd.MountInternal, assets.Mount)
		streamer = fd
	} else {
		logger.Warn("tv: the cluster schema (slot 0123) is missing; channels stream on this host only",
			"event", "tv_cluster_schema_missing")
		mounts = append(mounts, chHandler.Mount)
	}
	runEngineLoops(engine)

	// HDHomeRun emulation (Story 27.5): off until hdhr_device.enabled.
	hdhrRepo := hdhr.NewPGRepo(pool)
	hdhrHandler := hdhr.New(hdhrRepo, streamer)
	hdhrHandler.PublicBase = publicBase
	mounts = append(mounts, hdhrHandler.Mount)
	guideHandler := &hdhr.GuideHandler{Repo: hdhrRepo, PathMap: pathmap.FromEnv(),
		PublicBase: publicBase, WebBase: os.Getenv("MAKTABA_PUBLIC_WEB_BASE"),
		AheadFunc: func(ctx context.Context) time.Duration {
			return time.Duration(b.settings.TV(ctx).HorizonDays) * 24 * time.Hour
		}}
	mounts = append(mounts, guideHandler.Mount)
	// Only the home advertises the tuner (the front door); a node never runs this.
	ssdp := &hdhr.SSDPController{Repo: hdhrRepo, Logger: logger, LocationBase: publicBase}
	go ssdp.Run(context.Background())
	return mounts
}

func (b *tvBase) wireHomeCluster(ctx context.Context, engine *channel.Engine, local *channel.HTTPHandler,
	ident channel.HostIdentity, chRepo *channel.PGRepo) (*frontdoor.Handler, *cluster.AssetHandler) {
	logger := b.logger
	b.caps = cluster.DetectCapabilities(ctx, runtime.GOOS, b.cfg.FFmpeg.Binary, os.Getenv("MAKTABA_YTDLP"), nil)
	if err := cluster.EnsureSecret(ctx, b.pool); err != nil {
		logger.Warn("tv: creating the cluster stream key failed", "err", err.Error(), "event", "tv_secret_failed")
	}
	keyring := cluster.NewKeyring(nil)
	store := placement.PGStore{Pool: b.pool}
	csettings := &placement.PGSettings{Pool: b.pool}
	hosts := &placement.Hosts{Source: store, Settings: csettings, Protocol: cluster.ProtocolVersion, Logger: logger}
	svc := &placement.Service{Store: store, Hosts: hosts, Settings: csettings, Self: ident.ID, Logger: logger}
	tracker := frontdoor.NewTracker()
	tracker.WarmGrace = func() time.Duration { return b.settings.Output(context.Background()).WarmGrace }

	localRouter := chi.NewRouter()
	local.Mount(localRouter)
	fd := &frontdoor.Handler{Svc: svc, Settings: csettings, Self: ident.ID, Local: localRouter,
		Engine: engine, Keyring: keyring, Tracker: tracker, Logger: logger}

	b.loop = &cluster.HostLoop{Store: cluster.PGBeatStore{Pool: b.pool}, HostID: ident.ID, Keyring: keyring, Logger: logger,
		Info: func(ctx context.Context) cluster.BeatInfo {
			info := b.beatInfo(ctx, channel.RoleHome, engine, ident.StreamBaseURL, "")
			pins, last := svc.Notices()
			info.Load["viewers"] = tracker.Snapshot()
			if len(pins) > 0 {
				info.Load["pin_skips"] = pins
			}
			if last != nil {
				info.Load["no_room"] = last
			}
			return info
		}}
	// the first beat creates the home's row before the first decision
	_ = b.loop.BeatOnce(ctx)
	hosts.Refresh(ctx)
	go b.loop.Run(context.Background())

	engine.Fence = placement.HomeFence{Svc: svc}
	engine.HostStatusOff = true
	svc.OnChange(func(ch uuid.UUID) { go engine.CheckFences(context.Background(), ch) })
	go placement.Listen(context.Background(), b.pool, logger, func(ch uuid.UUID) {
		svc.Invalidate(ch)
		engine.CheckFences(context.Background(), ch)
	})
	sweeper := &placement.Sweeper{Svc: svc, Demand: tracker.Demand, Cleaner: func(ctx context.Context) {
		if n, err := store.CleanStaleRuntime(ctx); err == nil && n > 0 {
			logger.Info("tv: cleaned stale on-air rows", "rows", n, "event", "tv_runtime_cleaned")
		}
	}}
	go sweeper.Run(context.Background())

	// channel art for the stations on nodes
	assets := &cluster.AssetHandler{Looks: chRepo, PathMap: pathmap.FromEnv(), Keyring: keyring}
	logger.Info("tv: front door ready", "host_id", ident.ID, "event", "tv_frontdoor_ready")
	return fd, assets
}

// buildNodeTV is a node's whole public handler (AC3 of 32.1). A node whose code needs
// migrations the home's database lacks (cluster.SchemaGuard) streams nothing and does
// not beat: it answers 503 until the home caught up, then starts by itself.
func buildNodeTV(logger *slog.Logger, cfg config.Config, pool *pgxpool.Pool, layout *cache.Layout, ns cluster.NodeSettings) http.Handler {
	guard := &cluster.SchemaGuard{Q: pool, Logger: logger}
	if guard.Check(context.Background()) == "" {
		return buildNodeTVReady(logger, cfg, pool, layout, ns, guard)
	}
	sw := newSwapHandler(pausedNodeHandler())
	go func() {
		if guard.Wait(context.Background()) == nil {
			sw.set(buildNodeTVReady(logger, cfg, pool, layout, ns, guard))
		}
	}()
	return sw
}

// swapHandler serves the handler set last (a paused node's, then the real one).
type swapHandler struct{ h atomic.Pointer[http.Handler] }

func newSwapHandler(h http.Handler) *swapHandler {
	s := &swapHandler{}
	s.set(h)
	return s
}

func (s *swapHandler) set(h http.Handler) { s.h.Store(&h) }

func (s *swapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*s.h.Load()).ServeHTTP(w, r)
}

// pausedNodeHandler answers every request of a paused node.
func pausedNodeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		http.Error(w, "this node is paused: the home's database is older than its code", http.StatusServiceUnavailable)
	})
}

func buildNodeTVReady(logger *slog.Logger, cfg config.Config, pool *pgxpool.Pool, layout *cache.Layout,
	ns cluster.NodeSettings, guard *cluster.SchemaGuard) http.Handler {
	b := newTVBase(logger, cfg, pool, layout)
	ctx := context.Background()
	id := ns.NodeID
	if id == "" {
		id = "node-" + b.hostname
		logger.Warn("tv: node config has no node_id; using the host name", "event", "tv_node_no_id")
	}
	r := chi.NewRouter()
	if !clusterSchema(ctx, pool, 60*time.Second) {
		logger.Error("tv: the home's database has no cluster schema (slot 0123); this node streams nothing",
			"event", "tv_cluster_schema_missing")
		return r
	}
	b.caps = cluster.DetectCapabilities(ctx, runtime.GOOS, cfg.FFmpeg.Binary, os.Getenv("MAKTABA_YTDLP"), nil)
	keyring := cluster.NewKeyring(nil)
	store := placement.PGStore{Pool: pool}

	// the home's streaming address (its streaming_hosts row): art comes from there
	var homeBase atomic.Value
	homeBase.Store("")
	refreshHome := func() {
		hs, err := store.Hosts(ctx)
		if err != nil {
			return
		}
		for _, h := range hs {
			if h.IsHome() && h.BaseURL != "" {
				homeBase.Store(h.BaseURL)
			}
		}
	}
	refreshHome()

	// path map: MAKTABA_STREAM_PATH_MAP, else the cluster's map for this node
	var pm atomic.Value
	pm.Store(pathmap.FromEnv())
	var unfit atomic.Value
	unfit.Store("")
	// the home's schema stopped fitting while this node runs: it takes no channels
	var schemaUnfit atomic.Value
	schemaUnfit.Store("")
	checkSchema := func() {
		if guard.Check(ctx) != "" {
			schemaUnfit.Store(cluster.UnfitVersion)
		} else {
			schemaUnfit.Store("")
		}
	}
	checkStorage := func() {
		if os.Getenv(pathmap.EnvVar) == "" {
			qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if m, err := cluster.NodePathMap(qctx, pool, id); err == nil {
				pm.Store(m)
				// story 31.3: canonical smb:// paths resolve through it everywhere
				pathmap.SetDefault(m)
			}
			cancel()
		}
		qctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		u := cluster.StorageFit(qctx, pool, pm.Load().(pathmap.Map), nil)
		cancel()
		if prev := unfit.Swap(u); prev != u && u != "" {
			logger.Warn("tv: this node cannot read the library; it takes no channels", "event", "tv_node_unfit_storage")
		}
	}
	checkStorage()

	base := strings.TrimRight(os.Getenv("MAKTABA_PUBLIC_BASE_URL"), "/")
	if base == "" {
		if hb, _ := homeBase.Load().(string); hb != "" {
			base = cluster.StreamBaseURL(cluster.LocalAddrFor(hb), cfg.Server.Addr)
		}
	}
	ident := channel.HostIdentity{ID: id, Host: b.hostname, Role: channel.RoleNode, StreamBaseURL: base}
	chRepo := channel.NewPGRepo(pool, b.hostname)
	chRepo.HostID = id
	scfg := b.stationConfig(chRepo)
	scfg.PathMapSource = func() pathmap.Map { return pm.Load().(pathmap.Map) }
	scfg.Assets = &cluster.AssetCache{Base: func() string { s, _ := homeBase.Load().(string); return s },
		Keyring: keyring, Dir: filepath.Join(cfg.Cache.Root, "tv-assets")}
	start := channel.SettingsStarter(scfg, b.settings, b.encoders)
	engine := b.newEngine(chRepo, start, ident)
	engine.Fence = placement.NodeFence{Store: store, Self: id}
	engine.HostStatusOff = true
	watchChannels(engine, pool)

	b.loop = &cluster.HostLoop{Store: cluster.PGBeatStore{Pool: pool}, HostID: id, Keyring: keyring, Logger: logger,
		Info: func(ctx context.Context) cluster.BeatInfo {
			u := schemaUnfit.Load().(string)
			if u == "" {
				u = unfit.Load().(string)
			}
			return b.beatInfo(ctx, channel.RoleNode, engine, ident.StreamBaseURL, u)
		}}
	_ = b.loop.BeatOnce(ctx)
	go b.loop.Run(context.Background())
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			checkSchema()
			refreshHome()
			checkStorage()
		}
	}()
	go placement.Listen(context.Background(), pool, logger, func(ch uuid.UUID) {
		engine.CheckFences(context.Background(), ch)
	})
	runEngineLoops(engine)

	logger.Info("tv: streaming node ready", "host_id", id, "stream_base_url", base,
		"encoders", strings.Join(b.caps.Encoders, ","), "event", "tv_node_ready")
	cluster.MountNode(r, keyring, &channel.HTTPHandler{Engine: engine}, func() cluster.Health {
		ids := engine.ActiveIDs()
		active := make([]string, 0, len(ids))
		for _, x := range ids {
			active = append(active, x.String())
		}
		return cluster.Health{HostID: id, Version: version.Version, Protocol: cluster.ProtocolVersion,
			Active: active, Load: map[string]any{"cpu": b.cpu.Load()}}
	})
	return r
}
