package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	relayh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/relay"
	metricspkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/metrics"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/privacy"
	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

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
		func() (int, int) { n := registry.Len(); return n, n },
		retention.PurgeHourly,
		logger,
	)
	go runner.Run(ctx)

	r := server.NewRouter(logger, pool, mig, build, server.DefaultOrigins(cfg))

	r.Get("/v1/relay/ws", relaypkg.ServeWS(registry, serversStore))

	// All non-control paths fall through to the proxy. We deliberately
	// don't restrict by route prefix — the proxied server's URL space
	// is opaque to us.
	proxy := (&relayh.Deps{
		Registry:   registry,
		Servers:    serversStore,
		Meter:      meter,
		PublicHost: cfg.Relay.PublicHost,
		Collector:  collector,
	}).Handler()

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
	r.Get("/metrics", relayControl(metricspkg.PrometheusHandler(collector)))
	r.Get("/privacy", relayControl(privacy.PolicyHandler))

	r.HandleFunc("/*", proxy.ServeHTTP)

	srv := httpServer(cfg.Server.ListenAddr, r, cfg.Server.ReadTimeout, cfg.Server.WriteTimeout)
	if err := server.Run(ctx, logger, srv, cfg.Server.ShutdownGrace); err != nil {
		logger.Error("relay exited", "err", err)
		os.Exit(1)
	}
}
