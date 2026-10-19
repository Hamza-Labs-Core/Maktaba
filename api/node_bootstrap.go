package main

// Epic 35 phase C: the API as a full node's (stories 35.8–35.11, design §9–§12). A
// machine that joined a home runs the same maktaba-api in node mode (MAKTABA_NODE_MODE=1,
// set by its supervisor from node.json): the home's database through the node's own
// limited role (it refuses one that could run DDL, and never migrates), no home-only
// component (hub, updater, TV controller, publishers, secrets), sessions resolved by the
// home, every route served by its placement class, VOD read from the NAS here.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homelink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homeproxy"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/nodeauth"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/placement"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/schemaguard"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/vodfallback"
	grpcpipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
	grpcstreaming "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/streaming"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/clusterauth"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httpsec"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/nodemode"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/perf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/sharepath"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/system"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
)

// nodeRuntime is what a full node's API runs besides its routes.
type nodeRuntime struct {
	cfg      nodemode.Config
	db       *sql.DB
	link     *homelink.HTTPLink
	feed     *homelink.AgentFeed
	resolver *nodeauth.Resolver
	guard    *schemaguard.Guard
	vod      *vodfallback.Fallback
	home     *clusterauth.HomeInfoCache
	proxy    http.Handler
	rules    func() nodemode.Rules
	logger   *slog.Logger
}

// checkNodeEnv refuses what a node never does (story 35.8 AC2): migrate.
func checkNodeEnv(cfg nodemode.Config) error {
	if !cfg.Enabled {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MAKTABA_AUTO_MIGRATE")), "true") {
		return nodemode.ErrMigrate
	}
	return cfg.Validate()
}

// startNode checks the node's database role and starts its background work.
func startNode(ctx context.Context, cfg nodemode.Config, db *sql.DB, logger *slog.Logger) (*nodeRuntime, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := nodemode.CheckRole(cctx, db); err != nil {
		return nil, err
	}
	rt := &nodeRuntime{cfg: cfg, db: db, logger: logger}
	rt.link = &homelink.HTTPLink{Home: cfg.HomeAPIURL, NodeID: cfg.NodeID, Logger: logger}
	rt.home = &clusterauth.HomeInfoCache{Link: rt.link}

	var (
		rulesMu sync.Mutex
		rules   = nodemode.DefaultRules()
		rulesAt time.Time
	)
	rt.rules = func() nodemode.Rules {
		rulesMu.Lock()
		defer rulesMu.Unlock()
		if time.Since(rulesAt) > time.Minute {
			rctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			rules = nodemode.ReadRules(rctx, db)
			cancel()
			rulesAt = time.Now()
		}
		return rules
	}
	rt.resolver = &nodeauth.Resolver{Link: rt.link, Logger: logger,
		Grace: func() time.Duration { return time.Duration(rt.rules().GraceSeconds) * time.Second }}
	rt.link.Subscribe(rt.resolver.OnEvent)
	rt.guard = &schemaguard.Guard{DB: db, Logger: logger}
	rt.guard.Check(cctx)
	rt.proxy = homeproxy.New(rt.link, logger)

	if cfg.AgentURL != "" && cfg.AgentTokenFile != "" {
		rt.feed = &homelink.AgentFeed{URL: cfg.AgentURL, TokenFile: cfg.AgentTokenFile, Link: rt.link, Logger: logger}
		go rt.feed.Run(ctx)
	} else {
		logger.Warn("node: no machine agent configured (MAKTABA_AGENT_URL); this machine cannot act for users",
			"event", "node_no_agent")
	}
	go rt.resolver.Watch(ctx)
	go rt.guard.Run(ctx)
	// media through this machine's own mounts (design §11.5)
	sharepath.StartNode(ctx, db, cfg.NodeID, logger)
	streamMap := vodfallback.PrefixMap(os.Getenv("MAKTABA_STREAM_PATH_MAP"))
	rt.vod = &vodfallback.Fallback{DB: db, Home: rt.proxy, Logger: logger,
		ToLocal: func(p string) string { return streamMap(sharepath.ToLocal(p)) },
		HomeName: func() string {
			if s, ok := rt.home.Get(context.Background()); ok {
				return s.Name
			}
			return ""
		},
		OnChange: func(vodfallback.State) { go rt.report(context.Background()) },
	}
	system.SetMachine(system.Machine{Role: "node", NodeID: cfg.NodeID, WebURL: cfg.WebURL})
	go rt.reportLoop(ctx)
	logger.Info("node: full node mode", "node_id", cfg.NodeID, "home", cfg.HomeAPIURL, "web_url", cfg.WebURL,
		"event", "node_mode_started")
	return rt, nil
}

func platform() string {
	if p := os.Getenv("MAKTABA_PLATFORM"); p != "" {
		return p
	}
	return runtime.GOOS + "-" + runtime.GOARCH
}

// report tells the home what this machine is (cluster_node_report_full, slot 0135).
func (rt *nodeRuntime) report(ctx context.Context) {
	vod, _ := json.Marshal(rt.vod.Current())
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := rt.db.ExecContext(cctx, `SELECT cluster_node_report_full($1, $2, $3, $4, true, $5::jsonb)`,
		rt.cfg.NodeID, version.Version, platform(), rt.cfg.WebURL, string(vod)); err != nil {
		rt.logger.Debug("node: report to the home failed", "err", err, "event", "node_report_failed")
	}
}

func (rt *nodeRuntime) reportLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		rt.report(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// self is a node's GET /api/cluster/self.
func (rt *nodeRuntime) self(ctx context.Context) clusterauth.SelfInfo {
	info := clusterauth.SelfInfo{Role: "node", ID: rt.cfg.NodeID, Name: rt.cfg.Name, Version: version.Version,
		SchemaRev: schemaguard.SchemaRev, WebURL: rt.cfg.WebURL, Paused: rt.guard.Paused(), SSO: rt.rules().SSO}
	ok, since := rt.resolver.Reachability()
	home := &clusterauth.SelfHome{Reachable: ok, FailingSince: since}
	if hs, got := rt.home.Get(ctx); got {
		home.InstanceID, home.Name, home.WebURL, home.Version = hs.ID, hs.Name, hs.WebURL, hs.Version
		info.Machines = hs.Machines
		m := system.CurrentMachine()
		if m.HomeInstanceID != hs.ID && hs.ID != "" {
			m.HomeInstanceID = hs.ID
			system.SetMachine(m)
		}
	}
	info.Home = home
	if rt.feed != nil {
		c := rt.feed.Connected.Load() && rt.link.HasKey()
		info.AgentLink = &c
	}
	if st := rt.vod.Current(); st.Fallback {
		info.VOD = st
	}
	return info
}

// homeSelf is the home's GET /api/cluster/self: its identity, web address and the
// machines' addresses (the apps learn the cluster from any machine, story 35.11 EC2).
func homeSelf(store cluster.Store, webURL string) clusterauth.SelfFunc {
	return func(ctx context.Context) clusterauth.SelfInfo {
		info := clusterauth.SelfInfo{Role: "home", Version: version.Version, SchemaRev: schemaguard.SchemaRev,
			WebURL: webURL, SSO: true}
		if store == nil {
			return info
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if id, err := store.Identity(cctx); err == nil {
			info.ID, info.Name, info.AcceptJoins = id.InstanceID, id.Name, id.AcceptJoins
			info.Machines = append(info.Machines, clusterauth.SelfRow{Role: "home", ID: id.InstanceID, Name: id.Name, WebURL: webURL})
		}
		if nodes, err := store.Nodes(cctx); err == nil {
			for _, n := range nodes {
				if n.State != cluster.StateApproved || !n.FullNode {
					continue
				}
				row := clusterauth.SelfRow{Role: "node", ID: n.ID, Name: n.Name}
				if n.WebURL != nil {
					row.WebURL = *n.WebURL
				}
				info.Machines = append(info.Machines, row)
			}
		}
		return info
	}
}

// mountNodeRoutes mounts the node-only routes (/api/cluster/self, the sign-in's node
// half).
func (rt *nodeRuntime) mountNodeRoutes(r chi.Router) {
	router.MountNodeLocal(r, router.NodeLocalDeps{Self: rt.self, Node: &clusterauth.Node{
		Link: rt.link, NodeID: rt.cfg.NodeID, Self: rt.self, SecureCookies: cookiesSecure(), Logger: rt.logger,
		SSOEnabled: func(context.Context) bool { return rt.rules().SSO },
	}})
}

// handler is the node's public handler: the placement dispatcher over the local
// chain (home-resolved principal, the auth gate, CSRF per host, VOD) and the home proxy.
func (rt *nodeRuntime) handler(a *authState, r chi.Router, publicMux http.Handler) http.Handler {
	local := rt.vod.Wrap(publicMux)
	local = nodeauth.CSRF(local)
	local = middleware.RequireAuthExcept(middleware.DefaultPublicAllowlist())(local)
	local = rt.resolver.Middleware(local)
	local = middleware.AdminToken(a.adminToken)(local)
	local = mw.AuthRouteRateLimit(mw.DefaultAuthRouteLimits())(local)
	d := &placement.Dispatcher{Routes: r, Table: router.Placement, Local: local, Proxy: rt.proxy,
		Paused: rt.guard.Paused,
		Fixed: map[string]placement.Class{"/healthz": placement.Local,
			"/api/.well-known/jwks.json": placement.Home}}
	var h http.Handler = d
	h = httpsec.CORS(a.cors)(h)
	h = httpsec.Headers(a.headers)(h)
	return h
}

// warnUnclassified logs routes the placement table does not name (they are proxied to
// the home, the safe default).
func warnUnclassified(r chi.Router, logger *slog.Logger) {
	var missing []string
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if _, ok := router.Placement.Classify(method, route); !ok {
			missing = append(missing, method+" "+route)
		}
		return nil
	})
	if len(missing) > 0 {
		logger.Warn("node: routes without a placement class are proxied to the home",
			"routes", fmt.Sprint(missing), "event", "node_unclassified_routes")
	}
}

// mountHomeClusterAuth mounts the home's delegation and cluster sign-in endpoints and
// its /api/cluster/self (stories 35.9, 35.11).
func mountHomeClusterAuth(r chi.Router, db *sql.DB, logger *slog.Logger) {
	webURL := os.Getenv("MAKTABA_WEB_URL")
	router.MountClusterAuth(r, router.ClusterAuthDeps{DB: db, WebURL: webURL, Logger: logger})
	router.MountNodeLocal(r, router.NodeLocalDeps{Self: homeSelf(&cluster.PGStore{DB: db}, webURL)})
}

// mountNode opens the home's database with the node's role and mounts what a full
// node serves itself: the library and queue reads (`any`), the guide reads, this
// machine's routes (`local`); everything else is proxied to the home.
func mountNode(r chi.Router, cfg nodemode.Config, dataDir string, logger *slog.Logger) (*nodeRuntime, func(), error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, nil, fmt.Errorf("node mode needs the home's database (DATABASE_URL, the node role's from node.json)")
	}
	appDB, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("node mode: open the home's database: %w", err)
	}
	poolCfg := perf.DefaultPoolConfig()
	poolCfg.MaxOpen = envIntDefault("MAKTABA_DB_MAX_OPEN", 16)
	poolCfg.MaxIdle = envIntDefault("MAKTABA_DB_MAX_IDLE", 4)
	if perr := perf.ApplyPool(appDB, poolCfg); perr != nil {
		appDB.SetMaxOpenConns(poolCfg.MaxOpen)
		appDB.SetMaxIdleConns(poolCfg.MaxIdle)
	}
	ctx, cancel := context.WithCancel(context.Background())
	rt, err := startNode(ctx, cfg, appDB, logger)
	if err != nil {
		cancel()
		_ = appDB.Close()
		return nil, nil, err
	}
	var streamingClient grpcstreaming.Client
	if addr := os.Getenv("MAKTABA_STREAMING_ADDR"); addr != "" {
		c := grpcstreaming.DefaultConfig()
		c.Addr = addr
		streamingClient = grpcstreaming.NewRealClient(c)
	}
	var pipelineClient grpcpipeline.Client
	if addr := os.Getenv("MAKTABA_PIPELINE_ADDR"); addr != "" {
		c := grpcpipeline.DefaultConfig()
		c.Addr = addr
		pipelineClient = grpcpipeline.NewRealClient(c)
	}
	router.MountP6(r, router.P6Deps{
		DB: appDB, PipelineClient: pipelineClient, StreamingClient: streamingClient,
		BusCtx: ctx, BusDSN: dsn, PerfRegistry: perf.NewRegistry(),
		// this machine's own stream key: the node signs its own VOD sessions,
		// which its own streaming role verifies (Story 10.8, design §11.5)
		URLSigner: streamURLSigner(nil, logger),
	})
	router.MountP27(r, router.P27Deps{DB: appDB, PublicBaseURL: os.Getenv("MAKTABA_PUBLIC_BASE_URL"), DataDir: dataDir})
	rt.mountNodeRoutes(r)
	warnUnclassified(r, logger)
	logger.Info("node: local and any routes mounted; the rest is proxied to the home", "event", "node_mounted")
	return rt, func() { cancel(); _ = appDB.Close() }, nil
}
