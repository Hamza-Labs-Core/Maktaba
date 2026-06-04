package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
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
				noMDNS: noMDNS,
				noWeb:  noWeb,
				binDir: binDir,
				grace:  time.Duration(graceS) * time.Second,
			})
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "run a single role: api|streaming|pipeline|web (default: all)")
	cmd.Flags().BoolVar(&noMDNS, "no-mdns", false, "disable mDNS LAN advertisement")
	cmd.Flags().BoolVar(&noWeb, "no-web", false, "do not serve the embedded web UI in-process")
	cmd.Flags().StringVar(&binDir, "bin-dir", "", "directory to find sibling maktaba-api/maktaba-streaming binaries")
	cmd.Flags().IntVar(&graceS, "shutdown-grace", 30, "seconds to let children drain on shutdown")
	return cmd
}

type serveOptions struct {
	role   string
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
	for _, r := range roles {
		p, err := supervisor.Build(r, supervisor.Options{Config: cfg, BinDir: opts.binDir})
		if err != nil {
			return err
		}
		procs = append(procs, p)
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

	// Run children; on return (signal or child death), drain the web
	// server too.
	runErr := supervisor.Run(ctx, procs, opts.grace)
	if webSrv != nil {
		_ = shutdownWeb(webSrv)
	}
	return runErr
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
	if target := apiOrigin(cfg.Server.Listen); target != nil {
		proxy := httputil.NewSingleHostReverseProxy(target)
		mux.Handle("/api/", proxy)
	}

	mux.Handle("/", webui.Handler("/api/"))

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
