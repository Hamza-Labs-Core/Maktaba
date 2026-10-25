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

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/cache"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/relay"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/schemaguard"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/switchover"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
	clusterh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/system"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
)

// clusterTick is the Epic 31 housekeeping cadence: the accept-joins switch
// turns itself off after its hour, used sealed credentials are dropped.
const clusterTick = 30 * time.Second

// mountCluster wires Epic 31 (multi-instance): the /api/cluster routes,
// the instance identity row (name = this machine's stable host name on
// first start), the mDNS announcement (unless MAKTABA_DISCOVERY_MDNS=0)
// and the housekeeping loop. It returns a cleanup func for shutdown.
//
// store is the home's sealed secrets (nil: none); the extension relay
// reads the secrets it adds to the calls it relays from it.
//
// It also returns the verifier of the full nodes' signed requests (Epic 35 story 35.9:
// their delegation keys are issued on the channel and sealed in store).
func mountCluster(r chi.Router, db *sql.DB, dsn, publicAddr string, store secrets.Store, hubRef *router.HubRef,
	updatesRef *router.UpdatesRef, notifier notify.Notifier, logger *slog.Logger) (func(), *delegation.Verifier, *homeLink) {
	driver := "postgres"
	if isSQLiteDSN(dsn) {
		driver = "sqlite"
	}
	var pub discovery.Publisher
	// Epic 35 story 35.11: under the supervisor (maktaba-server serve) the supervisor
	// is the machine's one advertiser; the API announces only when it runs alone
	if mdnsEnabled() && os.Getenv("MAKTABA_SUPERVISED") != "1" {
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
		return func() {}, nil, nil
	}
	h.WebURL, h.Version = os.Getenv("MAKTABA_WEB_URL"), version.Version
	h.PGBundled, h.PGCA = os.Getenv("MAKTABA_CLUSTER_PG_PORT") != "", os.Getenv("MAKTABA_CLUSTER_PG_CA")
	// what keeps this home able to give its machines roles: the bundled Postgres's grants
	// made again by its supervisor (its superuser is the home's own), and a lost group role
	// made again by the home's own user
	if sc := switchover.SupervisorFromEnv(); h.PGBundled && sc != nil {
		h.MendBundled = func(ctx context.Context) error {
			cctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			return sc.Call(cctx, "mend-roles", struct{}{}, nil)
		}
	}
	if driver == "postgres" {
		h.RolesDB = db
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
		system.SetMachine(system.Machine{Role: "home", HomeInstanceID: id.InstanceID,
			WebURL: os.Getenv("MAKTABA_WEB_URL")})
	}
	go h.Run(ctx, clusterTick)

	// Epic 31 phase 2 — the cluster channel: every process's sealed
	// WebSocket, the browsers' hub, the cluster files, the LLM router.
	keys := &delegation.KeyStore{Secrets: store, DB: db, Logger: logger}
	if driver == "sqlite" {
		keys.DB = nil
	}
	// the cluster link (story 35.16): the home's CA, its certificates and the machines'
	// mutual-TLS channel on this port; a home without a CA keeps the older paths only
	var hl *homeLink
	var certs agent.CertIssuer
	if driver == "postgres" {
		name := ""
		if id.InstanceID != "" {
			name = id.Name
		}
		if hl = startHomeLink(ctx, db, store, h.Store, name, logger); hl != nil {
			hl.notifier = notifier
			certs = hl
			h.JoinTLS, h.Link = hl.JoinTLS, hl.info
			hl.mount(r)
			go hl.run(ctx)
		}
	}
	hub := mountClusterHub(r, h, db, driver, store, keys, certs, logger)
	if hl != nil {
		hl.skews = hub.ClockSkews
		// a revoked (or removed) machine loses its link and its certificates at once
		closeSessions := h.OnRevoke
		h.OnRevoke = func(nodeID string) {
			if closeSessions != nil {
				closeSessions(nodeID)
			}
			hl.revoke(nodeID)
		}
	}
	hubRef.Set(hub) // the update driver's machines (Epic 35 story 35.12)
	// a node's view on the Machines page carries its agent's last update.state (its login
	// to this home's database among it)
	h.LiveStates = hub.MachineStates
	// and a worker-only machine's what becomes of it (its next update converts it, or its
	// installer must run once: story 35.8 AC8), as the update driver knows it
	h.WorkerConvert = func(ctx context.Context) map[string]string {
		if uh := updatesRef.Get(); uh != nil && uh.Driver != nil {
			return uh.Driver.WorkerConvert(ctx)
		}
		return nil
	}
	// the machines verify this home's bundled Postgres with its CA, handed over the
	// machine channel (and sealed in a join's credentials); another database is said to
	// have no TLS when it has none
	if h.PGBundled {
		go hub.PushDatabase(ctx, h.PGCA, 30*time.Second, logger)
	} else if driver == "postgres" {
		go func() {
			if plainDatabase(ctx, db, 30*time.Second) {
				h.PGPlain.Store(true)
				hub.PushDatabase(ctx, "", 30*time.Second, logger)
			}
		}()
	}
	// the home owns its machines' database access: a machine that cannot log in to it
	// (its role missing or refused, the database moved to another cluster or port) gets
	// its access made again and pushed over the machine channel
	if driver == "postgres" {
		go func() {
			h.MendAtStart(ctx) // a home that may not give roles mends that before any machine asks
			if os.Getenv("MAKTABA_E2E_NO_ACCESS_PUSH") == "1" {
				// the end-to-end tests' home that never pushes: its machines ask
				hub.AnswerAccessRequests(h, logger)
				return
			}
			hub.EnsureAccess(ctx, h, 10*time.Second, logger)
		}()
	}
	// every revocation on the home reaches the full nodes within the second (story
	// 35.9 AC4): the session store reports it, the hub pushes auth.revoked
	sessions.SetRevokeHook(func(rv sessions.Revocation) {
		hub.BroadcastRevoked(agent.RevokedSessions{SessionIDs: rv.SessionIDs, UserID: rv.UserID, Reason: rv.Reason})
	})
	verifier := &delegation.Verifier{Keys: keys, Logger: logger, Member: func(ctx context.Context, nodeID string) bool {
		n, err := h.Store.Node(ctx, nodeID)
		return err == nil && n.State == cluster.StateApproved
	}}
	hubDone := make(chan struct{})
	go func() {
		defer close(hubDone)
		hub.Run(ctx)
	}()
	// switching the home to another machine (Epic 37): run here, finished on the new home
	if driver == "postgres" {
		sw := &switchover.Orchestrator{DB: db, Hub: hub, Logger: logger, HomeName: cluster.StableHostName(),
			SchemaRev: schemaguard.SchemaRev, ClusterKeyFile: agent.ClusterKeyPath(os.Getenv("MAKTABA_HOME_CLUSTER_KEY"))}
		if sc := switchover.SupervisorFromEnv(); sc != nil {
			sw.Supervisor = sc
		}
		sw.Mount(r)
		// a switch this home was running when it stopped, taken up (Epic 37)
		go sw.Resume(ctx)
		// the standby homes' kits of this home's secrets, kept current (Epic 37)
		go sw.KeepStandbys(ctx)
		stream := os.Getenv("MAKTABA_PUBLIC_BASE_URL")
		go sw.Finish(ctx, switchover.HomeIntegrations(db, store,
			switchover.HFTokenPath(os.Getenv("MAKTABA_DATA_DIR")), func() string { return stream }))
	}
	return func() {
		cancel()
		select {
		case <-hubDone:
		case <-time.After(5 * time.Second):
		}
		if pub != nil {
			_ = pub.Close()
		}
		sessions.SetRevokeHook(nil)
	}, verifier, hl
}

// mountClusterHub reads the hub's settings (agent.FromEnv), loads or
// creates the home's two local keys (never on the share): the home agent
// key (~/.maktaba/cluster/home-agent.key, MAKTABA_HOME_AGENT_KEY; mode
// MAKTABA_HOME_AGENT_KEY_MODE, 0600 or 0640) and the
// cluster key pair (~/.maktaba/cluster/home-cluster.key,
// MAKTABA_HOME_CLUSTER_KEY), whose public half goes into every approved
// machine's sealed credentials; then it mounts the hub.
func mountClusterHub(r chi.Router, h *clusterh.Handler, db *sql.DB, driver string, store secrets.Store,
	keys agent.KeyIssuer, certs agent.CertIssuer, logger *slog.Logger) *agent.Hub {
	cfg, warnings := agent.FromEnv(os.Getenv)
	for _, w := range warnings {
		logger.Warn("p31: cluster hub setting ignored", "detail", w, "event", "p31_hub_setting")
	}
	agentKeyEnv := os.Getenv("MAKTABA_HOME_AGENT_KEY")
	if agentKeyEnv == "" {
		agentKeyEnv = os.Getenv("MAKTABA_CLUSTER_KEY_PATH")
	}
	keyPath := agent.HomeKeyPath(agentKeyEnv)
	key, err := agent.LoadOrCreateKey(keyPath, cfg.HomeKeyMode)
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
	cfg.Delegation = keys
	cfg.Certs = certs
	if hc, ok := certs.(interface {
		HasCert(ctx context.Context, nodeID string) bool
	}); ok {
		cfg.HasCert = hc.HasCert
	}
	if db != nil {
		// the extension service door (story 31.R): nodes run the Plex
		// stages without the token; rules.relay switches it
		cfg.Relay = relay.New(relay.Config{Settings: relay.DBSettings(db),
			Upstream: relay.DBUpstream(db, store, os.Getenv), Logger: logger})
	}
	deps := router.P31AgentDeps{DB: db, Driver: driver, Config: cfg}
	var hub *agent.Hub
	if db != nil && driver != "sqlite" {
		// the database cache of the channel (design-agent.md §7.9): the hub
		// starts its Run and its first Backfill; later passes over the files
		// follow the hub's cluster directory. (The API's handle is a
		// Postgres one: a SQLite home gets no cache.)
		deps.Cache = cache.New(db, cache.Options{Window: cfg.CacheWindow, Driver: driver, Logger: logger,
			ClusterDir: func() string {
				if hub == nil {
					return ""
				}
				return hub.Location().Dir
			}})
	}
	hub = router.MountP31Agent(r, h, deps)
	logger.Info("p31: cluster channel mounted", "home_ip", hub.HomeIP(), "cache_hours", cfg.CacheWindow.Hours(),
		"event", "p31_hub_mounted")
	return hub
}

func mdnsEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("MAKTABA_DISCOVERY_MDNS")))
	return v != "0" && v != "false" && v != "off"
}

// plainDatabase reports whether this home's database has no TLS at all (`ssl` off):
// every machine's connection to it is plain then, whatever it asks for. It asks until
// the database answers, every interval; false once ctx ends.
func plainDatabase(ctx context.Context, db *sql.DB, every time.Duration) bool {
	for {
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var ssl string
		err := db.QueryRowContext(qctx, `SHOW ssl`).Scan(&ssl)
		cancel()
		if err == nil {
			return ssl == "off"
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(every):
		}
	}
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
