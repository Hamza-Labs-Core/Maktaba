package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	relayh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/relay"
	metricspkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/metrics"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/privacy"
	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
	tlspkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/tls"
)

// tlsAsk answers the edge's ask: GET ?domain=<host>, 200 to issue, 403 not.
func tlsAsk(policy tlspkg.HostPolicy) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("domain")))
		if host == "" || policy(r.Context(), host) != nil {
			http.Error(w, "no certificate for this host", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// editions is the web app's player and remote editions in dir (Epic 42.9), or nil when
// the relay has none.
func editions(logger *slog.Logger, dir string) *relayh.Editions {
	if dir == "" {
		return nil
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		logger.Warn("relay: no web editions", "dir", dir, "error", err)
		return nil
	}
	return &relayh.Editions{Root: os.DirFS(dir)}
}

// runRelay binds the relay role: a single listener that accepts both
// WebSocket upgrade requests from server agents at /v1/relay/ws AND
// HTTPS traffic at any other path, which is proxied through the
// matching tunnel.
//
// In production the listener sits behind Cloudflare/an ALB that
// terminates TLS and forwards Host header verbatim; subdomain routing
// happens entirely in user space here.
func runRelay(logger *slog.Logger, cfg config.Config, pool *db.Pool, mig *db.Migrator, build server.BuildInfo) {
	registry := relaypkg.NewRegistry()
	serversStore := stores.NewServers(pool.DB)
	meter := billingpkg.NewMeter(pool.DB)
	meter.Logger = logger
	meter.Start(context.Background())
	defer meter.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Epic 30 — aggregate-only relay analytics. The collector samples the
	// connection gauges + accumulates traffic counters; the runner flushes
	// per-minute rows, rolls them up hourly, and purges per the GDPR
	// retention windows (raw 24h, hourly 90d). All best-effort: a metrics
	// error is logged, never fatal to the relay.
	metricsStore := metricspkg.NewStore(pool.DB)
	collector := metricspkg.NewCollector()
	retention := privacy.NewRetention(pool.DB)
	runner := metricspkg.NewRunner(
		collector, metricsStore,
		registry.Counts,
		retention.PurgeHourly,
		logger,
	)
	go runner.Run(ctx)

	// Story 50.05: home lookups through a cache that outlives a database
	// failover, dropped at once by the database's change notifications.
	var cache *relayh.HomeCache
	if cfg.Relay.CacheEnabled {
		cache = &relayh.HomeCache{Lookup: serversStore}
		if err := stores.Listen(ctx, cfg.Database.URL, []string{stores.ChannelTierChanged, stores.ChannelServerChanged}, logger,
			func(_, slug string) { cache.Invalidate(slug) }, cache.Expire); err != nil {
			logger.Warn("relay: the change listener did not start; the cache refreshes each minute", "err", err)
		}
	}
	r := server.NewRouter(logger, pool, mig, build, server.DefaultOrigins(cfg), relayRouterOptions(cfg, cache, registry)...)

	// Story 50.03: resume tickets (the key derived from the token secret every
	// node shares) and DRAIN on shutdown, each with its switch.
	var tickets *relaypkg.Tickets
	if cfg.Relay.TicketsEnabled {
		tickets = relaypkg.NewTickets([]byte(cfg.Auth.TokenSecret))
	}
	// Story 50.04: with a peer address, this process records its tunnels in the
	// directory and forwards a request for a home whose tunnel is on another
	// process there (and serves its peers' forwards on the peer listener).
	relayID := relayIDOf(cfg)
	var directory relaypkg.Directory
	var peerAuth *relaypkg.PeerAuth
	if cfg.Relay.PeerAddr != "" {
		pg := relaypkg.NewPGDirectory(pool.DB, relayID, cfg.Relay.PeerAddr)
		pg.Draining = registry.Closing
		if err := pg.Start(ctx); err != nil {
			logger.Error("relay: the tunnel directory did not start", "err", err)
			os.Exit(1)
		}
		directory = pg
		peerAuth = relaypkg.NewPeerAuth([]byte(cfg.Auth.TokenSecret))
	}
	r.Get("/v1/relay/ws", (&relaypkg.TunnelServer{
		Registry:  registry,
		Store:     relaypkg.NewTunnelStore(serversStore),
		Logger:    logger,
		Tickets:   tickets,
		Drain:     cfg.Relay.DrainEnabled,
		Directory: directory,
	}).ServeHTTP)

	// All non-control paths fall through to the proxy. We deliberately
	// don't restrict by route prefix — the proxied server's URL space
	// is opaque to us.
	deps := relayh.Deps{
		Registry:   registry,
		Servers:    serversStore,
		Meter:      meter,
		PublicHost: cfg.Relay.PublicHost,
		Collector:  collector,
		Editions:   editions(logger, cfg.Relay.EditionsDir),
		Cache:      cache,
		RelayID:    relayID,
		Directory:  directory,
		PeerAuth:   peerAuth,
		HoldMax:    holdMax(cfg.Relay.HoldMax),
		HoldRecent: cfg.Relay.HoldRecent,
		Logger:     logger,
	}
	proxy := (&deps).Handler()
	if cfg.Relay.PeerAddr != "" {
		peerDeps := deps
		peerDeps.PeerMode = true
		peer := httpServer(cfg.Relay.PeerAddr, peerDeps.Handler(), cfg.Server.ReadTimeout, cfg.Server.WriteTimeout)
		go func() {
			logger.Info("relay: peer listener", "addr", cfg.Relay.PeerAddr, "relay_id", relayID)
			if err := peer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("relay: the peer listener failed", "err", err)
				os.Exit(1)
			}
		}()
		defer func() { _ = peer.Close() }()
	}

	// Observability + transparency endpoints, mounted BEFORE the proxy
	// catch-all so chi matches them first. Both are intentionally
	// auth-free: /metrics is scraped on the internal network, /privacy is
	// a public disclosure (Stories 30.4 / 30.2). A request to one of these
	// paths on a TENANT host (`<slug>.relay…`) is proxied to the home
	// server unchanged — the relay only answers them on its bare host, so
	// it never shadows a home server's own /metrics or /privacy.
	relayControl := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			if relayh.SlugFromHost(req.Host, cfg.Relay.PublicHost) != "" {
				proxy.ServeHTTP(w, req)
				return
			}
			h(w, req)
		}
	}
	r.Get("/metrics", relayControl(metricspkg.PrometheusHandler(collector, relaypkg.WriteMetrics, relayh.WriteMetrics, relayh.WriteCacheMetrics, meter.WriteMetrics)))
	r.Get("/privacy", relayControl(privacy.PolicyHandler))
	// Story 25.23: the edge's on-demand TLS asks here before it requests a
	// certificate (Caddy `on_demand_tls { ask … }`): 200 only for the bare
	// relay host and the subdomain of a claimed server, so random names
	// cannot burn the ACME quota.
	r.Get("/v1/relay/tls-ask", relayControl(tlsAsk(tlspkg.PolicyFromResolver(cfg.Relay.PublicHost, cfg.Relay.PublicHost, serversStore))))

	r.HandleFunc("/*", proxy.ServeHTTP)

	srv := httpServer(cfg.Server.ListenAddr, r, cfg.Server.ReadTimeout, cfg.Server.WriteTimeout)
	if err := server.RunDraining(ctx, logger, srv, relayGrace(cfg), relayDrain(logger, cfg, registry, tickets)); err != nil {
		logger.Error("relay exited", "err", err)
		os.Exit(1)
	}
}

// relayReadiness is the relay's /readyz rule (story 50.05): lenient while
// its cache can serve, unless relay.readyz_strict (or no cache).
func relayReadiness(cfg config.Config, cache *relayh.HomeCache) []server.RouterOption {
	if cfg.Relay.ReadyzStrict || cache == nil {
		return nil
	}
	return []server.RouterOption{server.WithLenientReadiness(&server.LenientReadiness{Warm: cache.Warm})}
}

// relayRouterOptions: the relay's readiness, and /readyz 503 "draining" while
// the registry shuts down (the edge then sends new work to the other process).
func relayRouterOptions(cfg config.Config, cache *relayh.HomeCache, reg *relaypkg.Registry) []server.RouterOption {
	return append(relayReadiness(cfg, cache), server.WithDraining(reg.Closing))
}

// relayIDOf names this relay process in the tunnel directory: relay.id, or
// the host name.
func relayIDOf(cfg config.Config) string {
	if cfg.Relay.ID != "" {
		return cfg.Relay.ID
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "relay"
}

// holdMax maps relay.hold_max to the handler's: 0 in the config turns
// holding off (the handler's 0 is its default).
func holdMax(d time.Duration) time.Duration {
	if d <= 0 {
		return -1
	}
	return d
}

// relayGrace is how long a stopping relay waits for its homes to move: the
// relay's own shutdown_grace (150 s), never less than the server's.
func relayGrace(cfg config.Config) time.Duration {
	return max(cfg.Relay.ShutdownGrace, cfg.Server.ShutdownGrace)
}

// relayDrain is what a stopping relay does before its listener closes (story
// 50.03): refuse new tunnels, send DRAIN to drain.rate homes a second, and
// wait for them to move. With relay.drain.enabled = false it closes every
// tunnel at once, as before.
func relayDrain(logger *slog.Logger, cfg config.Config, reg *relaypkg.Registry, tickets *relaypkg.Tickets) func(context.Context) {
	return func(ctx context.Context) {
		if !cfg.Relay.DrainEnabled {
			for _, t := range reg.All() {
				t.Close()
			}
			return
		}
		logger.Info("relay: draining tunnels", "tunnels", reg.Live(), "event", "relay_drain_start")
		reg.Shutdown(ctx, relaypkg.DrainOptions{
			Reason:   relaypkg.DrainShutdown,
			Within:   cfg.Relay.DrainWithin,
			Deadline: cfg.Relay.DrainDeadline,
		}, cfg.Relay.DrainRate, tickets.Mint)
		logger.Info("relay: tunnels drained", "left", reg.Live(), "event", "relay_drain_done")
	}
}
