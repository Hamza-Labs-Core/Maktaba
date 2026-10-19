package main

import (
	"context"
	"fmt"
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
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/webui"
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
			cfg, _, err := config.Load()
			if err != nil {
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The `web` role is served in-process; every other role is a
	// supervised subprocess. Decide which we're running.
	roles, serveWeb := resolveRoles(opts)

	// Start the in-process web server (and mDNS) when web is in scope.
	var webSrv *http.Server
	if serveWeb && !opts.noWeb {
		webSrv = startWebServer(cfg)
		fmt.Fprintf(os.Stderr, "[serve] web UI on %s (embedded=%v)\n", cfg.Server.WebListen, webui.Embedded)
	}

	if !opts.noMDNS && (serveWeb || containsRole(roles, supervisor.RoleAPI)) {
		port := mdns.PortFromListen(cfg.Server.Listen, 8080)
		if adv, err := mdns.Advertise("", port, version.Version); err != nil {
			fmt.Fprintf(os.Stderr, "[serve] mDNS advertisement failed (continuing): %v\n", err)
		} else {
			defer adv.Close()
			fmt.Fprintf(os.Stderr, "[serve] advertising _maktaba._tcp on port %d\n", port)
		}
	}

	// Build the supervised processes.
	var procs []supervisor.Process
	sopts := supervisor.Options{Config: cfg, BinDir: opts.binDir}
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
	sup := supervisor.New(procs, policy, supervisor.WithEventSink(
		supervisor.NewFileSink(filepath.Join(cfg.Server.DataDir, "supervisor", "events.jsonl"), 0)),
		supervisor.WithExitHook(supervisor.SlotExitReporter(cfg.Server.StreamAddr)))
	if adminSrv := startSupervisorAdmin(policy.AdminAddr, sup); adminSrv != nil {
		defer func() { _ = shutdownWeb(adminSrv) }()
	}
	runErr := sup.Run(ctx, opts.grace)
	if webSrv != nil {
		_ = shutdownWeb(webSrv)
	}
	return runErr
}

// startSupervisorAdmin serves the supervisor's state (GET /processes)
// on its loopback admin address; the api proxies it to
// /api/system/processes. A bind failure is logged, not fatal.
func startSupervisorAdmin(addr string, sup *supervisor.Supervisor) *http.Server {
	if addr == "" {
		return nil
	}
	srv := &http.Server{Addr: addr, Handler: sup.Handler(), ReadHeaderTimeout: 5 * time.Second}
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
func startWebServer(cfg config.Config) *http.Server {
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
		mux.Handle("/api/", proxy)
	}

	// Live TV through the web port: the channel HLS the player loads
	// (/stream/...) and the XMLTV guide + M3U lineup, proxied to the
	// streaming service so the browser stays on one origin. The guide
	// and lineup are public; /stream/ needs a signed-in session and only
	// the paths the web app loads (stream_gate.go).
	streamProxy := newStreamProxy(cfg.Server.StreamAddr)
	for _, p := range publicStreamPaths {
		mux.Handle(p, streamProxy)
	}
	if api != nil {
		mux.Handle("/stream/", newStreamGate(streamProxy, api))
	} else {
		mux.Handle("/stream/", http.NotFoundHandler())
	}

	mux.Handle("/", webui.Handler("/api/", "/stream/"))

	srv := &http.Server{
		Addr:              cfg.Server.WebListen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "[serve] web server error: %v\n", err)
		}
	}()
	return srv
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
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "live TV is not reachable: "+err.Error(), http.StatusBadGateway)
	}
	return proxy
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
