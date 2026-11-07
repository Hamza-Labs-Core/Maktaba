package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/direct"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/directjobs"
	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// runDNS is the direct-name domains' authoritative name server (Epic 50 story 50.15), on UDP and
// TCP 53 of nodes A and B. A and AAAA are computed from the name; the ACME challenges come from
// Postgres. It answers only names under direct.domains and refuses the rest.
func runDNS(logger *slog.Logger, cfg config.Config, pool *db.Pool, _ *db.Migrator, _ server.BuildInfo) {
	if len(cfg.Direct.Domains) == 0 {
		logger.Error("dns: direct.domains is empty: nothing to answer")
		os.Exit(2)
	}
	ns := [2]netip.Addr{}
	for i, s := range []string{cfg.Direct.NS1, cfg.Direct.NS2} {
		ip, err := netip.ParseAddr(s)
		if err != nil {
			logger.Error("dns: direct.ns1 and direct.ns2 are the nodes' IP addresses", "value", s)
			os.Exit(2)
		}
		ns[i] = ip
	}
	domains := cfg.Direct.Domains
	r := &direct.Responder{
		Domains: func() []string { return domains },
		NS:      ns,
		TXT:     stores.NewDirect(pool.DB),
		Limit:   &direct.RateLimit{PerSecond: 20, Burst: 50},
		Log:     logger,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pc, err := net.ListenPacket("udp", cfg.Direct.DNSListen)
	if err != nil {
		logger.Error("dns: listen udp", "addr", cfg.Direct.DNSListen, "err", err)
		os.Exit(1)
	}
	ln, err := net.Listen("tcp", cfg.Direct.DNSListen)
	if err != nil {
		logger.Error("dns: listen tcp", "addr", cfg.Direct.DNSListen, "err", err)
		os.Exit(1)
	}
	go func() {
		if err := r.ServeUDP(ctx, pc); err != nil {
			logger.Error("dns: udp", "err", err)
		}
	}()
	go func() {
		if err := r.ServeTCP(ctx, ln); err != nil {
			logger.Error("dns: tcp", "err", err)
		}
	}()
	logger.Info("dns: answering", "addr", cfg.Direct.DNSListen, "domains", domains, "event", "dns_up")

	// the control port, as the worker's: health for the node's checks
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","role":"dns"}`))
	})
	srv := &http.Server{Addr: "127.0.0.1:9091", Handler: mux, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	if err := server.Run(ctx, logger, srv, cfg.Server.ShutdownGrace); err != nil {
		logger.Error("dns exited", "err", err)
		os.Exit(1)
	}
}

// directHandler is the relay's handler for direct frames, or nil when direct access is off.
func directHandler(cfg config.Config, pool *db.Pool, logger *slog.Logger) relaypkg.DirectHandler {
	if !cfg.Direct.Enabled || len(cfg.Direct.Domains) == 0 {
		return nil
	}
	domains := cfg.Direct.Domains
	return &relaypkg.DBDirect{
		Store:   stores.NewDirect(pool.DB),
		Domains: func() []string { return domains },
		Budget:  cfg.Direct.Budget,
		Logger:  logger,
		Overflow: func(domain, ca string) {
			logger.Warn("direct: every domain's weekly budget is used: an overflow CA", "domain", domain, "ca", ca, "event", "direct_overflow")
		},
	}
}

// directJobs is the worker's certificates and checks, or nil when direct access is off.
func directJobs(cfg config.Config, pool *db.Pool, logger *slog.Logger) *directjobs.Jobs {
	if !cfg.Direct.Enabled {
		return nil
	}
	st := stores.NewDirect(pool.DB)
	cas := map[string]direct.CA{}
	for name, a := range cfg.Direct.ACME {
		if a.Directory == "" || a.AccountKey == "" {
			continue
		}
		key, err := direct.LoadOrCreateKey(a.AccountKey)
		if err != nil {
			logger.Error("direct: an ACME account key", "ca", name, "err", err)
			continue
		}
		cas[name] = direct.CA{Directory: a.Directory, Key: key, EABKeyID: a.EABKeyID, EABHMAC: a.EABHMAC, Contact: a.Contact}
	}
	return &directjobs.Jobs{
		Store:      st,
		Issuer:     &direct.Issuer{CAs: cas, Challenges: st},
		Checker:    direct.Checker{},
		CheckEvery: cfg.Direct.CheckEvery,
		Logger:     logger,
		Overflow: func(domain, ca string) {
			logger.Warn("direct: a first certificate from an overflow CA", "domain", domain, "ca", ca, "event", "direct_overflow")
		},
	}
}
