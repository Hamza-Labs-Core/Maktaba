package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/mdns"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/webui"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/pagetheme"
)

// allRoles is the default set started by a bare `serve`.
var allRoles = []supervisor.Role{
	supervisor.RoleAPI,
	supervisor.RoleStreaming,
	supervisor.RolePipeline,
}

func newServeCmd() *cobra.Command {
	var (
		role   string
		slot   string
		noMDNS bool
		noWeb  bool
		binDir string
		graceS int
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start all services (or a single role with --role)",
		Long: `serve starts the API (8080), streaming (8081), pipeline, and the
embedded web UI, advertising the server over mDNS for LAN discovery.

In container/orchestrated deployments, start one role per container:

  maktaba-server serve --role api
  maktaba-server serve --role streaming
  maktaba-server serve --role pipeline
  maktaba-server serve --role web`,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, found, err := config.Load()
			if err != nil {
				return err
			}
			if err := servePorts(&cfg, found, strings.TrimSpace(strings.ToLower(role)), noWeb); err != nil {
				return err
			}
			return runServe(cfg, serveOptions{
				role:   strings.TrimSpace(strings.ToLower(role)),
				slot:   strings.TrimSpace(strings.ToLower(slot)),
				noMDNS: noMDNS,
				noWeb:  noWeb,
				binDir: binDir,
				grace:  time.Duration(graceS) * time.Second,
			})
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "run a single role: api|streaming|pipeline|web|tv-slot (default: all)")
	cmd.Flags().StringVar(&slot, "slot", "", "with --role tv-slot: the instance, blue|green|amber")
	cmd.Flags().BoolVar(&noMDNS, "no-mdns", false, "disable mDNS LAN advertisement")
	cmd.Flags().BoolVar(&noWeb, "no-web", false, "do not serve the embedded web UI in-process")
	cmd.Flags().StringVar(&binDir, "bin-dir", "", "directory to find sibling maktaba-api/maktaba-streaming binaries")
	cmd.Flags().IntVar(&graceS, "shutdown-grace", 30, "seconds to let children drain on shutdown")
	return cmd
}

type serveOptions struct {
	role   string
	slot   string
	noMDNS bool
	noWeb  bool
	binDir string
	grace  time.Duration
}

func runServe(cfg config.Config, opts serveOptions) error {
	// Epic 35 story 35.8: a half-written node.json stops here, once, with its path,
	// rather than as every node role's crash loop.
	if err := supervisor.CheckNodeConfig(cfg); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// Under maktaba-launcher (Epic 35, story 35.2): every role starts from the
	// current release's directory, and the launcher's stop file stops us.
	releaseDir, err := launcherRelease()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	restart := watchLauncher(ctx, cancel, releaseDir)

	// The `web` role is served in-process; every other role is a
	// supervised subprocess. Decide which we're running.
	roles, serveWeb := resolveRoles(opts)

	// Epic 35 story 35.8: a joined node config makes this machine a full node (the
	// machine agent; the api in node mode; the web port's TV to the home).
	var node *nodeServices
	if containsRole(roles, supervisor.RoleAPI) {
		node = startNodeServices(ctx, cfg)
	}

	// Start the in-process web server (and mDNS) when web is in scope.
	var webSrv *http.Server
	if serveWeb && !opts.noWeb {
		webSrv, err = startWebServer(cfg, node)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "[serve] web UI on %s (embedded=%v)\n", cfg.Server.WebListen, webui.Embedded)
	}

	// Epic 35 story 35.11: the supervisor is the machine's one mDNS advertiser
	// (home or node), re-publishing when what it says changes.
	if !opts.noMDNS && (serveWeb || containsRole(roles, supervisor.RoleAPI)) {
		go advertiseMachine(ctx, cfg, node)
	}

	// Epic 36 story 36.1: the bundled Postgres (Maktaba.app) runs as the first
	// role of the whole machine, and every child gets its URL.
	pg, err := bundledPostgres(ctx, &cfg, opts.role == "" || opts.role == "all")
	if err != nil {
		return err
	}

	// The language model server (Maktaba.app's bundled Ollama, or [llm] url):
	// its URL goes to every child through cfg.
	llmProc, llmOpt, err := serveLLM(ctx, &cfg, opts.role == "" || opts.role == "all")
	if err != nil {
		return err
	}

	// Build the supervised processes.
	var procs []supervisor.Process
	if pg != nil {
		p, err := supervisor.BuildPostgres(*pg)
		if err != nil {
			return err
		}
		procs = append(procs, p)
		go pg.RunBackups(ctx, backupCheckInterval, os.Stderr)
	}
	if llmProc != nil {
		procs = append(procs, *llmProc)
	}
	sopts := supervisor.Options{Config: cfg, BinDir: opts.binDir, ReleaseDir: releaseDir}
	for _, r := range roles {
		if r == supervisor.RoleTVSlot {
			// a single slot instance (--role tv-slot --slot blue)
			p, err := supervisor.BuildSlot(opts.slot, sopts)
			if err != nil {
				return err
			}
			p.OnDemand = false
			procs = append(procs, p)
			continue
		}
		p, err := supervisor.Build(r, sopts)
		if err != nil {
			return err
		}
		procs = append(procs, p)
		if r == supervisor.RolePipeline {
			// [pipeline] instances = N (Epic 35 story 35.8): extra transcription workers
			extra, err := supervisor.BuildPipelineInstances(sopts)
			if err != nil {
				return err
			}
			procs = append(procs, extra...)
		}
		// Epic 33 story 33.1: the streaming role is the front door; the channel
		// engine runs in the TV slots next to it (live + hot standby; the third
		// on request)
		if r == supervisor.RoleStreaming && supervisor.SlotsEnabled(cfg) {
			slots, err := supervisor.BuildSlots(sopts)
			if err != nil {
				return err
			}
			procs = append(procs, slots...)
		}
	}

	// If only the web role was requested, there are no child processes;
	// block on the web server until signalled.
	if len(procs) == 0 {
		if webSrv == nil {
			return fmt.Errorf("nothing to run: --role web with --no-web leaves no services")
		}
		<-ctx.Done()
		return shutdownWeb(webSrv)
	}

	// Run children. Each one is restarted on its own when it crashes or
	// hangs ([supervisor] in server.toml, Story 22.9); Run returns on a
	// signal, or on a child exit with restart = "never". Then drain the
	// web server too.
	policy, err := supervisor.PolicyFromConfig(cfg.Supervisor)
	if err != nil {
		return err
	}
	supOpts := []supervisor.Option{
		supervisor.WithEventSink(supervisor.NewFileSink(filepath.Join(cfg.Server.DataDir, "supervisor", "events.jsonl"), 0)),
		supervisor.WithExitHook(supervisor.SlotExitReporter(cfg.Server.StreamAddr)),
	}
	if llmOpt != nil {
		supOpts = append(supOpts, llmOpt)
	}
	sup := supervisor.New(procs, policy, supOpts...)
	if adminSrv := startSupervisorAdmin(policy.AdminAddr, node.adminHandler(sup.Handler())); adminSrv != nil {
		defer func() { _ = shutdownWeb(adminSrv) }()
	}
	confirmPending(ctx, sup, releaseDir)
	runErr := sup.Run(ctx, opts.grace)
	if webSrv != nil {
		_ = shutdownWeb(webSrv)
	}
	if restart.Load() {
		os.Exit(exitRestart) // drained: the launcher starts current again
	}
	return runErr
}

// startSupervisorAdmin serves the supervisor's state (GET /processes)
// on its loopback admin address; the api proxies it to
// /api/system/processes. A bind failure is logged, not fatal.
func startSupervisorAdmin(addr string, h http.Handler) *http.Server {
	if addr == "" {
		return nil
	}
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "[serve] supervisor admin endpoint on %s failed (continuing): %v\n", addr, err)
		}
	}()
	fmt.Fprintf(os.Stderr, "[serve] supervisor state on http://%s/processes\n", addr)
	return srv
}

// resolveRoles maps the --role flag onto the supervised roles plus a
// flag for whether to serve the web UI in-process.
func resolveRoles(opts serveOptions) (roles []supervisor.Role, serveWeb bool) {
	switch opts.role {
	case "", "all":
		return allRoles, true
	case "web":
		return nil, true
	case string(supervisor.RoleAPI):
		return []supervisor.Role{supervisor.RoleAPI}, false
	case string(supervisor.RoleStreaming):
		return []supervisor.Role{supervisor.RoleStreaming}, false
	case string(supervisor.RolePipeline):
		return []supervisor.Role{supervisor.RolePipeline}, false
	case string(supervisor.RoleTVSlot):
		return []supervisor.Role{supervisor.RoleTVSlot}, false
	default:
		// Unknown role: treat as "all" but the build step will still
		// only launch the known set. Surface a hint.
		fmt.Fprintf(os.Stderr, "[serve] unknown --role %q; starting all roles\n", opts.role)
		return allRoles, true
	}
}

func containsRole(roles []supervisor.Role, want supervisor.Role) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

// startWebServer stands up the in-process SPA server. It serves the
// embedded UI and reverse-proxies /api to the API listen address so the
// whole app is reachable from a single origin (the web port).
//
// On a full node (node != nil) live TV, the guide and the lineup go to the home's front
// door, and a VOD session the home serves (the fallback) to the home's streaming port;
// VOD stays on this machine's streaming role (Epic 35 design §11.3).
func startWebServer(cfg config.Config, node *nodeServices) (*http.Server, error) {
	srv := &http.Server{
		Addr:              cfg.Server.WebListen,
		Handler:           webHandler(cfg, node),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Bind before returning: a web port that cannot be bound stops serve with the
	// reason instead of a log line nobody reads.
	ln, err := net.Listen("tcp", cfg.Server.WebListen)
	if err != nil {
		return nil, fmt.Errorf("web UI cannot listen on %s (web_listen in %s): %w", cfg.Server.WebListen, config.Path(), err)
	}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "[serve] web server error: %v\n", err)
		}
	}()
	return srv, nil
}

// servePorts checks the binds serve is about to use. Serving every role, a port
// another program holds moves into the fallback range and server.toml is updated
// (config/ports.go); a port another running Maktaba holds stops serve. A single
// role (containers, orchestrators) never moves a port: a taken one stops serve.
func servePorts(cfg *config.Config, found bool, role string, noWeb bool) error {
	var keys []string
	single := true
	switch role {
	case "", "all":
		single = false
		keys = []string{"listen", "stream_addr"}
		if !noWeb {
			keys = append(keys, "web_listen")
		}
	case "web":
		keys = []string{"web_listen"}
	case string(supervisor.RoleAPI):
		keys = []string{"listen"}
	case string(supervisor.RoleStreaming):
		keys = []string{"stream_addr"}
	default:
		return nil
	}
	o := config.PortOptions{Keys: keys, Wait: servePortWait}
	if single {
		probe := *cfg
		moved, err := probe.ReconcilePorts(portProbe(), o)
		if err != nil {
			return err
		}
		if len(moved) > 0 {
			return fmt.Errorf("%s %s is in use by another program: free it or set %s in %s",
				moved[0].Key, moved[0].From, moved[0].Key, config.Path())
		}
		return nil
	}
	moved, err := reconcilePorts(cfg, config.Path(), o, os.Stderr, found)
	if err == nil && len(moved) > 0 && !found {
		fmt.Fprintf(os.Stderr, "  port: no server.toml at %s; the new address(es) hold for this run only\n", config.Path())
	}
	return err
}

// webHandler is the web port: the embedded UI, /api/ proxied to the API
// and live TV proxied to the streaming service. Every page it renders
// itself (errors, "restarting") follows the system theme (pagetheme).
func webHandler(cfg config.Config, node *nodeServices) http.Handler {
	mux := http.NewServeMux()

	// Reverse-proxy /api/* to the API service so the SPA can talk to a
	// same-origin backend. Best-effort: if the target is unparseable we
	// simply don't mount the proxy and the SPA points elsewhere.
	api := apiOrigin(cfg.Server.Listen)
	if api != nil {
		proxy := httputil.NewSingleHostReverseProxy(api)
		apiDirector := proxy.Director
		proxy.Director = func(r *http.Request) {
			apiDirector(r)
			// the web port is never the relay (Epic 32.9): only the relay
			// agent marks its loopback calls X-Maktaba-Via: relay
			r.Header.Del("X-Maktaba-Via")
		}
		// this machine's API is down or restarting: a themed page for a browser
		proxy.ErrorHandler = apiUnreachable
		mux.Handle("/api/", proxy)
	}

	// Live TV through the web port: the channel HLS the player loads
	// (/stream/...) and the XMLTV guide + M3U lineup, proxied to the
	// streaming service so the browser stays on one origin. The guide
	// and lineup are public; /stream/ needs a signed-in session and only
	// the paths the web app loads (stream_gate.go).
	streamProxy := newStreamProxy(cfg.Server.StreamAddr)
	publicProxy, gated := streamProxy, streamProxy
	allow := allowedStreamPath
	if node != nil {
		home := homeStreamProxy(supervisor.HomeStreamURL(node.node))
		publicProxy = home
		gated = nodeStreamRouter(streamProxy, home)
		allow = allowedNodeStreamPath
	}
	for _, p := range publicStreamPaths {
		mux.Handle(p, publicProxy)
	}
	if api != nil {
		gate := newStreamGate(gated, api)
		gate.allow = allow
		mux.Handle("/stream/", gate)
	} else {
		mux.Handle("/stream/", http.HandlerFunc(pagetheme.NotFound))
	}

	mux.Handle("/", webui.Handler("/api/", "/stream/"))
	return mux
}

// publicStreamPaths are the web-port paths the streaming service serves
// without a session (/stream/ goes through the streamGate).
var publicStreamPaths = []string{"/xmltv.xml", "/lineup.m3u"}

// newStreamProxy reverse-proxies to the streaming service on this host.
// FlushInterval -1 flushes every write: live playlists and MPEG-TS must
// not sit in a buffer.
func newStreamProxy(streamAddr string) http.Handler {
	target := &url.URL{Scheme: "http", Host: supervisor.LocalAddr(streamAddr, "8081")}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		// the web session is for the API only, never the streaming service
		r.Header.Del("Cookie")
		r.Header.Del("Authorization")
	}
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if pagetheme.WantsHTML(r) {
			pagetheme.Write(w, pagetheme.Page{
				Status:       http.StatusBadGateway,
				Title:        "Live TV unavailable · Maktaba",
				Heading:      "Live TV is not reachable",
				Message:      "The streaming service on this server is not answering. It may be starting or restarting.",
				Detail:       err.Error(),
				RetrySeconds: 10,
				Links:        []pagetheme.Link{{Href: "/", Text: "Back to Maktaba"}},
			})
			return
		}
		http.Error(w, "live TV is not reachable: "+err.Error(), http.StatusBadGateway)
	}
	return proxy
}

// apiUnreachable answers a /api/ request the API did not take (it is
// down, starting or restarting): a browser that navigated there (a report
// page, a link) gets a themed page that retries; the web app's fetches
// get a problem+json body they can show.
func apiUnreachable(w http.ResponseWriter, r *http.Request, err error) {
	fmt.Fprintf(os.Stderr, "[serve] api proxy: %s %s: %v\n", r.Method, r.URL.Path, err)
	if pagetheme.WantsHTML(r) {
		pagetheme.Write(w, pagetheme.Page{
			Status:       http.StatusBadGateway,
			Title:        "Maktaba is restarting",
			Heading:      "Maktaba is starting up",
			Message:      "The Maktaba API on this server is not answering yet. It is usually starting, restarting or updating; this page reloads by itself.",
			Detail:       err.Error(),
			RetrySeconds: 5,
			Links:        []pagetheme.Link{{Href: "/", Text: "Back to Maktaba"}},
		})
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Retry-After", "5")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = fmt.Fprintf(w, `{"type":"about:blank","title":"API unreachable","status":502,"detail":%q}`,
		"the Maktaba API is not answering (starting or restarting)")
}

// apiOrigin turns the API bind spec into a dialable loopback URL for the
// reverse proxy (a 0.0.0.0 bind is reachable via 127.0.0.1 on-host).
func apiOrigin(listen string) *url.URL {
	port := mdns.PortFromListen(listen, 8080)
	u, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err != nil {
		return nil
	}
	return u
}

func shutdownWeb(srv *http.Server) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
