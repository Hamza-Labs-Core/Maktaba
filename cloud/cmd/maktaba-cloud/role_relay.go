package main

import (
	"context"
	"log/slog"
	"os"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	relayh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/relay"
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
	}).Handler()
	r.HandleFunc("/*", proxy.ServeHTTP)

	srv := httpServer(cfg.Server.ListenAddr, r, cfg.Server.ReadTimeout, cfg.Server.WriteTimeout)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Run(ctx, logger, srv, cfg.Server.ShutdownGrace); err != nil {
		logger.Error("relay exited", "err", err)
		os.Exit(1)
	}
}
