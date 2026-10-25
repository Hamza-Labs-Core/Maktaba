package main

// Epic 35 phase C: the API as a full node's (stories 35.8–35.11, design §9–§12). A
// machine that joined a home runs the same maktaba-api in node mode (MAKTABA_NODE_MODE=1,
// set by its supervisor from node.json): the home's database through the node's own
// limited role (it refuses one that could run DDL, and never migrates), no home-only
// component (hub, updater, TV controller, publishers, secrets), sessions resolved by the
// home, every route served by its placement class, VOD read from the NAS here.
//
// Epic 37 (specs/epics/37-node-replica): the node has its own live, read-only copy of
// the home's database (MAKTABA_COPY_DATABASE_URL, its supervisor's): the node's reads go
// to it, its writes to the home's (nodedb), and with the home away it signs people in
// and answers them from it (nodeauth.Local), plays from it, and even starts so: a role
// check the home's database cannot answer (it does not answer, starts up, has no
// connection to spare, refuses for now) waits for it; only a role found able to run
// DDL stops the API, at start or later.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homelink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homeproxy"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/link"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/nodeauth"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/nodedb"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/nodemetrics"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/placement"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/schemaguard"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/switchover"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/vodfallback"
	grpcpipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
	grpcstreaming "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/streaming"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/clusterauth"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httpsec"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/nodemode"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/perf"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/sharepath"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/system"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/updates"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
)

// nodeDBs are a full node's databases: the pair its handlers use (reads on the copy,
// writes on the home's), the home's alone (the role check) and the copy alone (sign-ins
// from it; nil: no copy).
type nodeDBs struct {
	pair   *nodedb.Pair
	app    *sql.DB
	home   *sql.DB
	copyDB *sql.DB
}

// openNodeDBs opens them from the home's DSN (the node role's) and the copy's ("": none).
func openNodeDBs(homeDSN, copyDSN string, logger *slog.Logger) (nodeDBs, error) {
	// the node's dsn says sslmode=prefer, which lib/pq does not take: pgdsn settles it;
	// bounded: a home switched off leaves a statement hanging, cut when its request ends
	homeConn, err := pgdsn.BoundedConnector(homeDSN)
	if err != nil {
		return nodeDBs{}, fmt.Errorf("node mode: the home's database: %w", err)
	}
	d := nodeDBs{pair: &nodedb.Pair{Home: &nodedb.Source{Name: "home", Conn: homeConn}, Logger: logger},
		home: sql.OpenDB(homeConn)}
	if copyDSN != "" {
		copyConn, err := pgdsn.Connector(copyDSN)
		if err != nil {
			_ = d.home.Close()
			return nodeDBs{}, fmt.Errorf("node mode: this machine's copy of the home's database: %w", err)
		}
		d.pair.Copy = &nodedb.Source{Name: "copy", Conn: copyConn}
		d.copyDB = sql.OpenDB(copyConn)
		d.copyDB.SetMaxOpenConns(4)
	}
	d.app = d.pair.Open(time.Minute)
	return d, nil
}

func (d nodeDBs) close() {
	for _, db := range []*sql.DB{d.app, d.home, d.copyDB} {
		if db != nil {
			_ = db.Close()
		}
	}
}

// nodeRuntime is what a full node's API runs besides its routes.
type nodeRuntime struct {
	cfg  nodemode.Config
	dbs  nodeDBs
	link *homelink.HTTPLink
	feed *homelink.AgentFeed
	// linkC is the machine's cluster link (story 35.16; nil without a certificate folder)
	linkC    *link.Client
	resolver *nodeauth.Resolver
	guard    *schemaguard.Guard
	vod      *vodfallback.Fallback
	home     *clusterauth.HomeInfoCache
	proxy    http.Handler
	rules    func() nodemode.Rules
	logger   *slog.Logger
	// metrics: the node's delegation, placement and pause numbers (story 35.15)
	metrics *nodemetrics.Metrics
	// recent: who changed something the copy may not have yet (their reads go to the
	// home's); clock: the home's clock, which the copy's beat is stamped on
	recent nodedb.Recent
	clock  homeClock
	// fresh: whether the copy is current enough to read from while the home answers
	fresh copyFreshness
	// fatal carries an error the API must stop for (a DDL-capable role found late)
	fatal chan error
	// bg is the work whose last save the API waits for at shutdown
	bg sync.WaitGroup
}

// homeDownAfter is how long the home must not answer before a video this machine
// cannot read is said to be unavailable (one unanswered probe is not an outage).
const homeDownAfter = 2 * nodeauth.ProbeEvery

// waitBG waits (at most d) for the background work's last save.
func (rt *nodeRuntime) waitBG(d time.Duration) {
	done := make(chan struct{})
	go func() { rt.bg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		rt.logger.Warn("node: the last save of this machine's state did not finish in time", "event", "node_state_save_late")
	}
}

// nodeOffline is a full node's way to play with the home's database down
// (streaming.Offline, vodfallback.Catalog): the copy answers for the videos.
type nodeOffline struct{ dbs nodeDBs }

func (o nodeOffline) DBDown() bool { return o.dbs.pair.Home.Down() }

// LedgerPath keeps the sessions this machine opened, until the home's database has
// them, in its state directory.
func (o nodeOffline) LedgerPath() string {
	if dir := nodeStateDir(); dir != "" {
		return filepath.Join(dir, "stream-sessions.json")
	}
	return ""
}
func (o nodeOffline) DBFailed(error) { o.dbs.pair.Recheck() }

// WhenDBBack calls fn each time the home's database answers again.
func (o nodeOffline) WhenDBBack(fn func()) {
	o.dbs.pair.WhenUp(func(s *nodedb.Source) {
		if s == o.dbs.pair.Home {
			fn()
		}
	})
}

func (o nodeOffline) Video(id string) (string, float64, bool) {
	var lib string
	var dur sql.NullFloat64
	if !o.row(`SELECT library_id, duration_sec FROM videos WHERE id = $1`, id, &lib, &dur) {
		return "", 0, false
	}
	return lib, dur.Float64, true
}

func (o nodeOffline) Path(id string) (string, bool) {
	var p sql.NullString
	return p.String, o.row(`SELECT path FROM videos WHERE id = $1`, id, &p) && p.String != ""
}

func (o nodeOffline) row(q, id string, dest ...any) bool {
	if o.dbs.copyDB == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return o.dbs.copyDB.QueryRowContext(ctx, q, id).Scan(dest...) == nil
}

// nodeStateDir is where a node keeps its own state (its node-local sign-ins, when the
// home last answered): MAKTABA_NODE_STATE_DIR (its supervisor sets it), else
// <MAKTABA_DATA_DIR>/node, else none (kept in memory only).
func nodeStateDir() string {
	if d := os.Getenv("MAKTABA_NODE_STATE_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("MAKTABA_DATA_DIR"); d != "" {
		return filepath.Join(d, "node")
	}
	return ""
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

// boundNodeDatabaseURL gives a full node's DATABASE_URL, the home's database across
// the network, a connect_timeout (pgdsn.ConnectTimeout) unless it has one: lib/pq
// takes a context for the dial at most, so a home that accepts and never answers held
// the role check below (its 15 s), before anything bound, and every later connection,
// for as long as the socket lived.
func boundNodeDatabaseURL(cfg nodemode.Config) {
	if dsn := os.Getenv("DATABASE_URL"); cfg.Enabled && dsn != "" {
		_ = os.Setenv("DATABASE_URL", pgdsn.WithConnectTimeout(dsn, pgdsn.ConnectTimeout))
	}
}

// startNode checks the node's database role and starts its background work.
func startNode(ctx context.Context, cfg nodemode.Config, dbs nodeDBs, logger *slog.Logger) (*nodeRuntime, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rt := &nodeRuntime{cfg: cfg, dbs: dbs, logger: logger, fatal: make(chan error, 1)}
	if dir := nodeStateDir(); dir != "" {
		rt.clock.keepAt(filepath.Join(dir, "home-clock.json"))
	}
	rt.recent.Now = rt.clock.Now
	roleErr := nodemode.CheckRole(cctx, dbs.home)
	checkLater := false
	switch {
	case roleErr == nil:
	case errors.Is(roleErr, nodemode.ErrRoleCanDDL):
		return nil, roleErr
	default:
		logger.Warn("node: the home's database cannot be asked yet; starting without it, on this machine's copy (the role is checked once it answers)",
			"err", roleErr, "unreachable", nodemode.Unreachable(roleErr), "copy", dbs.copyDB != nil,
			"event", "node_start_without_db")
		dbs.pair.Recheck()
		checkLater = true
	}
	dir := nodeStateDir()
	if dir == "" {
		logger.Warn("node: no state directory (MAKTABA_NODE_STATE_DIR): sign-ins made here are not kept across restarts",
			"event", "node_state_memory_only")
	}
	inDir := func(name string) string {
		if dir == "" {
			return ""
		}
		return filepath.Join(dir, name)
	}
	rt.link = &homelink.HTTPLink{Home: cfg.HomeAPIURL, NodeID: cfg.NodeID, Logger: logger}
	// the machine's cluster link carries the home calls while it is up (story 35.16)
	if rt.linkC = startNodeLink(ctx, cfg.HomeAPIURL, cfg.NodeID, cfg.CertDir, os.Getenv("MAKTABA_STREAMING_HTTP_URL"),
		logger); rt.linkC != nil {
		rt.link.Link = rt.linkC
	}
	rt.home = &clusterauth.HomeInfoCache{Link: rt.link}

	rt.rules = nodemode.CachedRules(dbs.app, time.Minute)
	minutes := func(n int) time.Duration { return time.Duration(n) * time.Minute }
	// a full node's metrics only: a home and a worker have none of these series
	rt.metrics = nodemetrics.Register(func() string { return rt.guard.Paused() })
	rt.resolver = &nodeauth.Resolver{Link: rt.link, Logger: logger, SecureCookies: cookiesSecure(), Metrics: rt.metrics,
		Grace:             func() time.Duration { return time.Duration(rt.rules().GraceSeconds) * time.Second },
		CopyMaxLag:        func() time.Duration { return minutes(rt.rules().OfflineCopyMaxLagMinutes) },
		LocalSessionTTL:   func() time.Duration { return time.Duration(rt.rules().OfflineSessionHours) * time.Hour },
		LocalSessionGrace: func() time.Duration { return minutes(rt.rules().OfflineSessionGraceMinutes) },
		Sessions:          &nodeauth.LocalSessions{Path: inDir(nodeauth.LocalSessionsFile)},
		SignOuts:          &nodeauth.SignOuts{Path: inDir(nodeauth.SignOutsFile)},
		HomeOffset:        rt.clock.Offset,
	}
	if dbs.copyDB != nil {
		rt.resolver.Local = clusterauth.NewCopy(dbs.copyDB, cfg.NodeID)
	} else {
		logger.Warn("node: no copy of the home's database (MAKTABA_COPY_DATABASE_URL); with the home away no one is signed in here",
			"event", "node_no_copy")
	}
	if err := rt.resolver.Sessions.Load(); err != nil {
		logger.Warn("node: the sign-ins made here are unreadable; starting without them", "err", err,
			"event", "node_local_sessions_unreadable")
	}
	if err := rt.resolver.SignOuts.Load(); err != nil {
		logger.Warn("node: the sign-outs made here are unreadable; starting without them", "err", err,
			"event", "node_sign_outs_unreadable")
	}
	// the revocations the home pushed, while they are honoured (a restart in an outage,
	// when the home cannot push them again, does not forget them)
	if p := inDir(nodeauth.RevokedFile); p != "" {
		rt.resolver.KeepRevoked(p)
	}
	rt.link.Subscribe(rt.resolver.OnEvent)
	if p := inDir(nodeauth.HomeSeenFile); p != "" {
		rt.resolver.LoadHomeSeen(p)
		rt.bg.Add(1)
		go func() { defer rt.bg.Done(); rt.resolver.KeepHomeSeen(ctx, p) }()
	}
	rt.guard = &schemaguard.Guard{DB: dbs.app, Logger: logger}
	rt.guard.Check(cctx)
	if checkLater {
		go rt.checkRoleLater(ctx)
	}
	// the home's database answers again: its schema is read at once (it may have been
	// updated meanwhile), not at the next round
	dbs.pair.WhenUp(func(s *nodedb.Source) {
		if s == dbs.pair.Home {
			rt.guard.Nudge()
		}
	})
	// a session probe or a sign-in the home does not answer is answered here, from the
	// copy; a home not answering its probes is not waited on for long
	rt.proxy = homeproxy.NewWith(rt.link, logger, homeproxy.Options{Fallback: rt.resolver.Fallback,
		Failing: func() bool { return rt.resolver.DownFor(0) }})

	if cfg.AgentURL != "" && cfg.AgentTokenFile != "" {
		rt.feed = &homelink.AgentFeed{URL: cfg.AgentURL, TokenFile: cfg.AgentTokenFile, Link: rt.link, Logger: logger}
		go rt.feed.Run(ctx)
	} else {
		logger.Warn("node: no machine agent configured (MAKTABA_AGENT_URL); this machine cannot act for users",
			"event", "node_no_agent")
	}
	go rt.resolver.Watch(ctx)
	go rt.guard.Run(ctx)
	go dbs.pair.Watch(ctx, 5*time.Second)
	// media through this machine's own mounts (design §11.5): the map from the copy
	// while the home's database does not answer
	sharepath.StartNode(ctx, dbs.app, cfg.NodeID, logger)
	streamMap := vodfallback.PrefixMap(os.Getenv("MAKTABA_STREAM_PATH_MAP"))
	// what the home serves goes through the resolver's front (a node-local sign-in is
	// answered here, never sent to the home, which would sign the person out)
	rt.vod = &vodfallback.Fallback{DB: dbs.app, Home: rt.resolver.Home(rt.proxy), Logger: logger,
		ToLocal: func(p string) string { return streamMap(sharepath.ToLocal(p)) },
		HomeName: func() string {
			if s, ok := rt.home.Get(context.Background()); ok {
				return s.Name
			}
			return ""
		},
		HomeDown: func() bool { return rt.resolver.DownFor(homeDownAfter) },
		Catalog:  rt.offline(),
		OnChange: func(vodfallback.State) { go rt.report(context.Background()) },
	}
	system.SetMachine(system.Machine{Role: "node", NodeID: cfg.NodeID, WebURL: cfg.WebURL})
	go rt.reportLoop(ctx)
	logger.Info("node: full node mode", "node_id", cfg.NodeID, "home", cfg.HomeAPIURL, "web_url", cfg.WebURL,
		"copy", dbs.copyDB != nil, "event", "node_mode_started")
	return rt, nil
}

func platform() string {
	if p := os.Getenv("MAKTABA_PLATFORM"); p != "" {
		return p
	}
	return runtime.GOOS + "-" + runtime.GOARCH
}

func (rt *nodeRuntime) offline() nodeOffline { return nodeOffline{dbs: rt.dbs} }

// checkRoleLater runs the start's role check until the home's database answers it: a
// role found able to run DDL stops the API then (rt.fatal), as it would have at start;
// any other failure (it does not answer, starts up, refuses for now) is tried again,
// and said once per kind.
func (rt *nodeRuntime) checkRoleLater(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	said := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := nodemode.CheckRole(cctx, rt.dbs.home)
		cancel()
		switch {
		case err == nil:
			rt.logger.Info("node: the home's database answers; this machine's role is checked", "event", "node_role_checked")
			rt.guard.Nudge()
			return
		case errors.Is(err, nodemode.ErrRoleCanDDL):
			rt.fatal <- err
			return
		case ctx.Err() != nil || nodemode.Unreachable(err):
		case err.Error() != said:
			said = err.Error()
			rt.logger.Warn("node: the home's database refuses this machine's role for now; tried again", "err", err,
				"event", "node_role_check_refused")
		}
	}
}

// report tells the home what this machine is (cluster_node_report_full, slot 0135).
func (rt *nodeRuntime) report(ctx context.Context) {
	vod, _ := json.Marshal(rt.vod.Current())
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := rt.dbs.home.ExecContext(cctx, `SELECT cluster_node_report_full($1, $2, $3, $4, true, $5::jsonb)`,
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
	if dir := nodeStateDir(); dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, copyStatusFile)); err == nil {
			info.Copy = publicCopy(b)
		}
	}
	return info
}

// publicCopy is the copy's status as /api/cluster/self (public) shows it: its state and
// how far it has applied the home's changes; never its detail (errors name roles and
// paths: the home's Machines page shows them to its admins).
func publicCopy(raw []byte) json.RawMessage {
	var s struct {
		State     string     `json:"state"`
		AppliedAt *time.Time `json:"applied_at,omitempty"`
		LagMS     *int64     `json:"lag_ms,omitempty"`
		Tables    int        `json:"tables,omitempty"`
		Ready     int        `json:"ready,omitempty"`
		Checked   *time.Time `json:"checked,omitempty"`
	}
	if json.Unmarshal(raw, &s) != nil || s.State == "" {
		return nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	return b
}

// copyStatusFile is the supervisor's status of this machine's copy (nodecopy.StatusFile).
const copyStatusFile = "copy.json"

// homeAutoUpdates is whether this home installs releases by itself (rules.updates
// auto_install as this build applies it, off with MAKTABA_UPDATE_DISABLE), read at most
// every 30 s (/api/cluster/self is asked often).
func homeAutoUpdates(db *sql.DB) func(context.Context) bool {
	var mu sync.Mutex
	var at time.Time
	var on bool
	return func(ctx context.Context) bool {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(at) < 30*time.Second {
			return on
		}
		if v := os.Getenv("MAKTABA_UPDATE_DISABLE"); v == "1" || strings.EqualFold(v, "true") {
			on, at = false, time.Now()
			return on
		}
		p, err := updates.LoadPolicy(ctx, db, version.Channel(), os.Getenv)
		if err != nil {
			return on // the last word until the database answers
		}
		on, at = p.EffectiveAutoInstall(), time.Now()
		return on
	}
}

// homeSelf is the home's GET /api/cluster/self: its identity, web address and the
// machines' addresses (the apps learn the cluster from any machine, story 35.11 EC2), and
// whether it installs releases by itself (autoUpdates; MountSelf says it to this machine
// only).
func homeSelf(store cluster.Store, webURL string, term func(context.Context) int64,
	offDB func() []agent.OffDatabase, autoUpdates func(context.Context) bool) clusterauth.SelfFunc {
	return func(ctx context.Context) clusterauth.SelfInfo {
		info := clusterauth.SelfInfo{Role: "home", Version: version.Version, SchemaRev: schemaguard.SchemaRev,
			WebURL: webURL, SSO: true}
		if store == nil {
			return info
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if autoUpdates != nil {
			on := autoUpdates(cctx)
			info.UpdatesAuto = &on
		}
		if term != nil {
			info.Term = term(cctx)
		}
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
		if offDB != nil {
			names := map[string]string{}
			for _, m := range info.Machines {
				names[m.ID] = m.Name
			}
			for _, o := range offDB() {
				info.MachinesDB = append(info.MachinesDB, clusterauth.SelfMachineDB{ID: o.NodeID, Name: names[o.NodeID],
					Code: o.Code, Healing: o.Healing})
			}
		}
		return info
	}
}

// mountNodeRoutes mounts the node-only routes (/api/cluster/self, the sign-in's node
// half).
func (rt *nodeRuntime) mountNodeRoutes(r chi.Router) {
	// a switch's notices of the new home: from the copy (it answers with the home away),
	// else the home's database
	movedDB := rt.dbs.copyDB
	if movedDB == nil {
		movedDB = rt.dbs.home
	}
	// a standby home taking over when its home is gone: its supervisor checks and does it
	em := &switchover.Emergency{}
	if sc := switchover.SupervisorFromEnv(); sc != nil {
		em.Supervisor = sc
	}
	// the machine's own processes reach the home on its cluster link (story 35.16)
	r.Get(link.PathLocalAgent, serveLocalAgent(rt.linkC))
	r.Get(link.PathLocalFetch, serveLocalFetch(rt.linkC))
	router.MountNodeLocal(r, router.NodeLocalDeps{Self: rt.self, Node: &clusterauth.Node{
		Link: rt.link, NodeID: rt.cfg.NodeID, Self: rt.self, SecureCookies: cookiesSecure(), Logger: rt.logger,
		SSOEnabled: func(context.Context) bool { return rt.rules().SSO },
	}, Moved: switchover.MovedHandler(movedDB), EmergencyStatus: em.Status, Emergency: em.Start, TermClaim: em.Claim})
}

// handler is the node's public handler: the placement dispatcher over the local
// chain (the principal, the auth gate, CSRF per host, VOD, reads on the copy) and the
// home proxy (with the sign-in this machine answers from its copy while the home is
// away, rate-limited as the home's own).
func (rt *nodeRuntime) handler(a *authState, r chi.Router, publicMux http.Handler) http.Handler {
	local := rt.dbDownReads(rt.vod.Wrap(publicMux))
	local = nodeauth.CSRF(local)
	local = middleware.RequireAuthExcept(middleware.DefaultPublicAllowlist())(local)
	local = rt.resolver.Middleware(local)
	local = middleware.AdminToken(a.adminToken)(local)
	limit := mw.AuthRouteRateLimit(mw.DefaultAuthRouteLimits())
	local = rt.copyReads(limit(local))
	home := rt.copyReads(rt.limitOfflineSignIn(limit, rt.resolver.Home(rt.proxy)))
	d := &placement.Dispatcher{Routes: r, Table: router.Placement, Local: local, Proxy: home,
		Paused: rt.guard.Paused,
		Served: func(c placement.Class, status int) { rt.metrics.Served(string(c), status) },
		Fixed: map[string]placement.Class{"/healthz": placement.Local,
			"/api/.well-known/jwks.json": placement.Home}}
	var h http.Handler = d
	h = httpsec.CORS(a.cors)(h)
	h = httpsec.Headers(a.headers)(h)
	return h
}

// copyReads sends a request's reads to this machine's copy (nodedb.CopyReads): a GET
// or HEAD the node serves itself, unless its author changed something a moment ago
// (their reads go to the home's database until the copy has the change), or the copy
// is further behind the home than rules.cluster.copy_reads_max_lag_seconds (then every
// read goes to the home's while it answers: a change of rights or a removal is not
// read stale). Any other request is a change: its author's reads go to the home's for
// a moment.
func (rt *nodeRuntime) copyReads(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := nodeauth.CredentialKey(r)
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			if rt.copyMayRead(r.Context()) && !rt.recent.Changed(key, rt.fresh.syncedAt()) {
				r = r.WithContext(nodedb.CopyReads(r.Context()))
			}
		case http.MethodOptions:
		default:
			// as it starts (reads meanwhile go to the home), and once it is done: the
			// copy has it once it holds a beat after that
			rt.recent.Wrote(key)
			next.ServeHTTP(w, r)
			rt.recent.Wrote(key)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// copyMayRead reports whether a read may go to the copy: while the home answers, only
// as long as the copy is current (copyFresh); while it does not (its API, or its
// database), always: the copy falls behind in an outage, and the home cannot be read.
func (rt *nodeRuntime) copyMayRead(ctx context.Context) bool {
	if (rt.resolver != nil && rt.resolver.Down()) || (rt.dbs.pair != nil && rt.dbs.pair.Home.Down()) {
		return true
	}
	return rt.copyFresh(ctx)
}

// copyFreshness is the last verdict on the copy's lag (read at most every
// copyFreshEvery).
type copyFreshness struct {
	mu     sync.Mutex
	at     time.Time
	fresh  bool
	lag    time.Duration
	synced time.Time // the copy's beat, on the home's clock
	said   bool      // the copy was said to be behind (logged once per spell)
}

// syncedAt is the copy's beat as last read (the home's clock).
func (f *copyFreshness) syncedAt() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.synced
}

const copyFreshEvery = 2 * time.Second

// copyFresh reports whether the copy may answer the node's reads while the home
// answers: it applied the home's changes up to no more than the rule's lag ago (the
// home's beat, written every few seconds while a machine follows it, against the
// home's clock).
func (rt *nodeRuntime) copyFresh(ctx context.Context) bool {
	f := &rt.fresh
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.at.IsZero() && time.Since(f.at) < copyFreshEvery {
		return f.fresh
	}
	f.at = time.Now()
	if rt.resolver == nil || rt.resolver.Local == nil {
		f.fresh = false
		return false
	}
	lctx, cancel := context.WithTimeout(ctx, time.Second)
	synced, err := rt.resolver.Local.Synced(lctx)
	cancel()
	rt.clock.measure(ctx, rt.dbs.home)
	limit := time.Duration(rt.rules().CopyReadsMaxLagSeconds) * time.Second
	f.synced = synced
	f.lag = rt.clock.Now().Sub(synced)
	f.fresh = err == nil && !synced.IsZero() && f.lag <= limit
	switch {
	case !f.fresh && !f.said:
		f.said = true
		rt.logger.Warn("node: this machine's copy is behind the home; reads go to the home's database meanwhile",
			"lag", f.lag.Round(time.Second).String(), "max", limit.String(), "err", err, "event", "node_copy_reads_off")
	case f.fresh && f.said:
		f.said = false
		rt.logger.Info("node: this machine's copy has caught up; reads go to it again", "event", "node_copy_reads_on")
	}
	return f.fresh
}

// limitOfflineSignIn rate-limits every sign-in through this machine: the ones it answers
// itself (the home away, in its grace, or the proxy's fallback when the home did not
// answer) as the ones it sends on (which the home limits too).
func (rt *nodeRuntime) limitOfflineSignIn(limit func(http.Handler) http.Handler, next http.Handler) http.Handler {
	limited := limit(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == nodeauth.LoginPath {
			limited.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// dbDownReads answers an `any` route (a read of the home's database) at once while that
// database does not answer and this machine's copy cannot stand in (none, or it does
// not answer either): 503 home_unreachable rather than a request that hangs on a dead
// connection. The app keeps its notice (pages that read the database wait for it,
// playback does not); `local` routes go on.
func (rt *nodeRuntime) dbDownReads(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := rt.dbs.pair
		if p == nil || !p.Home.Down() || (p.Copy != nil && !p.Copy.Down()) ||
			placement.ClassFrom(r.Context()) != placement.Any {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set(nodeauth.HomeStateHeader, "unreachable")
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://maktaba.dev/problems/home-unreachable",
			"title": "the home's database is not reachable", "status": http.StatusServiceUnavailable,
			"code":   "home_unreachable",
			"detail": "this page reads the home's database, which does not answer; videos this machine can read still play"})
	})
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
// its /api/cluster/self (stories 35.9, 35.11), and runs its side of the machines'
// copies of its database (Epic 37).
func mountHomeClusterAuth(r chi.Router, db *sql.DB, hubRef *router.HubRef, logger *slog.Logger) {
	webURL := os.Getenv("MAKTABA_WEB_URL")
	router.MountClusterAuth(r, router.ClusterAuthDeps{DB: db, WebURL: webURL, Logger: logger})
	offDB := func() []agent.OffDatabase {
		if h := hubRef.Get(); h != nil {
			return h.MachinesOffDatabase()
		}
		return nil
	}
	router.MountNodeLocal(r, router.NodeLocalDeps{Self: homeSelf(&cluster.PGStore{DB: db}, webURL,
		func(ctx context.Context) int64 { return switchover.TermOf(ctx, db) }, offDB, homeAutoUpdates(db)),
		Moved: switchover.MovedHandler(db)})
	// the machines' copies of this database (Epic 37): the beat, the CA, the cap, the
	// slots guarded; on a database that is not Maktaba's own Postgres, none without the rule
	// (read at each beat, its errors told apart: a rule not read ends no copy)
	rules := nodemode.CachedRules(db, time.Minute)
	go (&cluster.Copies{DB: db, CAFile: os.Getenv("MAKTABA_CLUSTER_PG_CA"), Logger: logger,
		Bundled: os.Getenv("MAKTABA_CLUSTER_PG_PORT") != "",
		KeepGB:  func() int { return rules().ReplicaWALKeepGB },
		OperatorPG: func(ctx context.Context) (bool, error) {
			r, err := nodemode.LoadRules(ctx, db)
			return r.CopiesOnOperatorPostgres, err
		}}).Run(context.Background())
}

// mountNode opens the home's database with the node's role, and this machine's copy of
// it, and mounts what a full node serves itself: the library and queue reads (`any`),
// the guide reads, this machine's routes (`local`); everything else is proxied to the
// home.
func mountNode(r chi.Router, cfg nodemode.Config, dataDir string, logger *slog.Logger) (*nodeRuntime, func(), error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, nil, fmt.Errorf("node mode needs the home's database (DATABASE_URL, the node role's from node.json)")
	}
	dbs, err := openNodeDBs(dsn, os.Getenv("MAKTABA_COPY_DATABASE_URL"), logger)
	if err != nil {
		return nil, nil, err
	}
	appDB := dbs.app
	poolCfg := perf.DefaultPoolConfig()
	poolCfg.MaxOpen = envIntDefault("MAKTABA_DB_MAX_OPEN", 16)
	poolCfg.MaxIdle = envIntDefault("MAKTABA_DB_MAX_IDLE", 4)
	if perr := perf.ApplyPool(appDB, poolCfg); perr != nil {
		appDB.SetMaxOpenConns(poolCfg.MaxOpen)
		appDB.SetMaxIdleConns(poolCfg.MaxIdle)
	}
	ctx, cancel := context.WithCancel(context.Background())
	rt, err := startNode(ctx, cfg, dbs, logger)
	if err != nil {
		cancel()
		dbs.close()
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
		// the event bus's pq.Listener takes a connection string (lib/pq has no
		// connector-based listener), so its sslmode is settled here at startup, and
		// again when the listener cannot connect (the database down at startup leaves
		// it at require), not per connection as appDB's is
		BusCtx: ctx, BusDSN: pgdsn.ForLibPQ(ctx, dsn, pgdsn.Ping), PerfRegistry: perf.NewRegistry(),
		BusResettle: func(ctx context.Context) string { return pgdsn.ForLibPQ(ctx, dsn, pgdsn.Ping) },
		// this machine's own stream key: the node signs its own VOD sessions,
		// which its own streaming role verifies (Story 10.8, design §11.5)
		URLSigner: streamURLSigner(nil, logger),
		// the home's database down: this machine's copy and session record
		StreamOffline: rt.offline(),
	})
	router.MountP27(r, router.P27Deps{DB: appDB, PublicBaseURL: os.Getenv("MAKTABA_PUBLIC_BASE_URL"), DataDir: dataDir})
	rt.mountNodeRoutes(r)
	warnUnclassified(r, logger)
	logger.Info("node: local and any routes mounted; the rest is proxied to the home", "event", "node_mounted")
	return rt, func() { cancel(); rt.waitBG(3 * time.Second); dbs.close() }, nil
}
