package main

// A full node's own TV while its home is away (specs/epics/37-node-replica): the
// channel engine on this machine's copy of the home's database, so the node's web port
// can send its viewers here (`/stream/offline/stream/channel/{id}/…`) when it cannot reach
// the home's front door. A channel's position comes from its schedule and the clock,
// as on the home (the copy has both), so a viewer sees what the home would show. The
// station is unfenced (nothing places channels with the home away) and answers this
// machine's web port only (loopback); its writes (channel_runtime) fail on the copy and
// are dropped, as such a write is anywhere. The art the home serves is not fetched.

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cache"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/channel"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pathmap"
)

// offlinePrefix is the offline station's routes on the streaming port.
const offlinePrefix = "/stream/offline"

// openCopy is a pool on this machine's copy of the home's database (MAKTABA_COPY_DATABASE_URL;
// nil: none). It connects when first used and closes with bg.
func openCopy(bg *background, logger *slog.Logger) *pgxpool.Pool {
	dsn := os.Getenv(copyEnv)
	if dsn == "" {
		logger.Warn("streaming: no copy of the home's database (MAKTABA_COPY_DATABASE_URL); with the home away nothing plays",
			"event", "stream_no_copy")
		return nil
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		logger.Warn("streaming: this machine's copy of the home's database is not usable", "err", err.Error(),
			"event", "stream_copy_unusable")
		return nil
	}
	bg.OnStop(pool.Close)
	return pool
}

// buildOfflineTV is the offline station (nil without a copy).
func buildOfflineTV(bg *background, logger *slog.Logger, cfg config.Config, copyPool *pgxpool.Pool) http.Handler {
	if copyPool == nil {
		return nil
	}
	layout := cache.New(filepath.Join(cfg.Cache.Root, "offline"))
	if err := layout.EnsureTiers(); err != nil {
		logger.Warn("cache layout init failed", "root", layout.Root, "err", err.Error(), "event", "cache_init_warn")
	}
	b := newTVBase(bg, logger, cfg, copyPool, layout)
	b.readOnly = true
	repo := channel.NewPGRepo(copyPool, b.hostname)
	scfg := b.stationConfig(repo)
	// this machine's path map, from the copy while the home's database does not answer
	scfg.PathMapSource = pathmap.Default
	start := channel.SettingsStarter(scfg, b.settings, b.encoders)
	// no host id: no channel is "owned elsewhere" (the home that places them is away)
	engine := b.newEngine(repo, start, channel.HostIdentity{Host: b.hostname, Role: channel.RoleNode})
	engine.HostStatusOff = true
	runEngineLoops(bg, engine)
	r := chi.NewRouter()
	(&channel.HTTPHandler{Engine: engine}).Mount(r)
	logger.Info("tv: this machine's own channels, for its viewers while the home is away", "event", "tv_offline_ready")
	return loopbackOnly(http.StripPrefix(offlinePrefix, r))
}

// loopbackOnly answers this machine's own programs only (the web port's proxy).
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isOffline reports whether a path is the offline station's.
func isOffline(p string) bool { return strings.HasPrefix(p, offlinePrefix+"/") }
