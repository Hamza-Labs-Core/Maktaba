package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
	clusterh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
)

// clusterTick is the Epic 31 housekeeping cadence: the accept-joins switch
// turns itself off after its hour, used sealed credentials are dropped.
const clusterTick = 30 * time.Second

// mountCluster wires Epic 31 (multi-instance): the /api/cluster routes,
// the instance identity row (name = this machine's stable host name on
// first start), the mDNS announcement (unless MAKTABA_DISCOVERY_MDNS=0)
// and the housekeeping loop. It returns a cleanup func for shutdown.
func mountCluster(r chi.Router, db *sql.DB, dsn, publicAddr string, logger *slog.Logger) func() {
	driver := "postgres"
	if isSQLiteDSN(dsn) {
		driver = "sqlite"
	}
	var pub discovery.Publisher
	if mdnsEnabled() {
		pub = discovery.NewZeroconfPublisher()
	}
	var adv *cluster.Advertiser
	if pub != nil {
		adv = &cluster.Advertiser{
			Publisher: pub,
			Port:      portOf(publicAddr, 8080),
			Version:   version.Version,
			SchemaRev: schemaRev(db, driver),
		}
	}
	h := router.MountP31(r, router.P31Deps{
		DB:         db,
		Driver:     driver,
		Advertiser: adv,
		PGPort:     envIntDefault("MAKTABA_CLUSTER_PG_PORT", 5432),
		Logger:     logger,
	})
	if h == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	bctx, bcancel := context.WithTimeout(ctx, 10*time.Second)
	id, err := h.Bootstrap(bctx, cluster.StableHostName())
	bcancel()
	if err != nil {
		logger.Warn("p31: instance identity bootstrap failed", "err", err, "event", "p31_identity_failed")
	} else {
		logger.Info("p31: cluster handlers mounted", "instance", id.Short(), "mdns", pub != nil,
			"event", "p31_mounted")
	}
	go h.Run(ctx, clusterTick)

	// Epic 31 phase 2 — the cluster channel: every process's sealed
	// WebSocket, the browsers' hub, the cluster files, the LLM router.
	hub := mountClusterHub(r, h, db, driver, logger)
	hubDone := make(chan struct{})
	go func() {
		defer close(hubDone)
		hub.Run(ctx)
	}()
	return func() {
		cancel()
		select {
		case <-hubDone:
		case <-time.After(5 * time.Second):
		}
		if pub != nil {
			_ = pub.Close()
		}
	}
}

// mountClusterHub reads the hub's settings (agent.FromEnv), loads or
// creates the home's two local keys (never on the share): the home agent
// key (~/.maktaba/cluster/home-agent.key, MAKTABA_HOME_AGENT_KEY) and the
// cluster key pair (~/.maktaba/cluster/home-cluster.key,
// MAKTABA_HOME_CLUSTER_KEY), whose public half goes into every approved
// machine's sealed credentials; then it mounts the hub.
func mountClusterHub(r chi.Router, h *clusterh.Handler, db *sql.DB, driver string, logger *slog.Logger) *agent.Hub {
	cfg, warnings := agent.FromEnv(os.Getenv)
	for _, w := range warnings {
		logger.Warn("p31: cluster hub setting ignored", "detail", w, "event", "p31_hub_setting")
	}
	agentKeyEnv := os.Getenv("MAKTABA_HOME_AGENT_KEY")
	if agentKeyEnv == "" {
		agentKeyEnv = os.Getenv("MAKTABA_CLUSTER_KEY_PATH")
	}
	keyPath := agent.HomeKeyPath(agentKeyEnv)
	key, err := agent.LoadOrCreateHomeKey(keyPath)
	if err != nil {
		logger.Warn("p31: home agent key unavailable; the home's own processes cannot join the channel",
			"path", keyPath, "err", err, "event", "p31_home_key_failed")
	}
	cfg.HomeKey = key
	clusterPath := agent.ClusterKeyPath(os.Getenv("MAKTABA_HOME_CLUSTER_KEY"))
	if priv, pub, err := agent.LoadOrCreateClusterKey(clusterPath); err != nil {
		logger.Warn("p31: home cluster key unavailable; machines cannot join the channel",
			"path", clusterPath, "err", err, "event", "p31_cluster_key_failed")
	} else {
		cfg.ClusterKey = priv
		h.ClusterPub = cluster.EncodeKey(pub)
	}
	cfg.Logger = logger
	hub := router.MountP31Agent(r, h, router.P31AgentDeps{DB: db, Driver: driver, Config: cfg})
	logger.Info("p31: cluster channel mounted", "home_ip", hub.HomeIP(), "event", "p31_hub_mounted")
	return hub
}

func mdnsEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("MAKTABA_DISCOVERY_MDNS")))
	return v != "0" && v != "false" && v != "off"
}

// portOf extracts the port of a listen address (":8080", "0.0.0.0:8080").
func portOf(addr string, def int) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return def
	}
	n, err := strconv.Atoi(p)
	if err != nil || n <= 0 || n > 65535 {
		return def
	}
	return n
}

// schemaRev is MAKTABA_SCHEMA_REV, else the latest applied migration slot.
func schemaRev(db *sql.DB, driver string) int {
	if n := envIntDefault("MAKTABA_SCHEMA_REV", 0); n > 0 || db == nil || driver != "postgres" {
		return n
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int64
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&n); err != nil {
		return 0
	}
	return int(n)
}
