package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// healthcheck: a container's health (Epic 59 story 59.10), what the TrueNAS app's compose
// runs (`HEALTHCHECK`-style, `docker exec`, so with the image's own binary and its
// environment). The image carries no curl. Healthy:
//   - while the container waits for its first setup (no server.toml): it is running as it
//     should, and a NAS that waits for healthy before it shows the app's Shell would
//     otherwise never let the household run that setup;
//   - once set up, when the web app's public health route (/api/system/health on
//     server.toml's listen) answers 200.
//
// Unhealthy otherwise (exit 1), with the reason on stdout, which Docker keeps in the
// container's health log.

// healthTimeout bounds the one request.
const healthTimeout = 5 * time.Second

func newHealthcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "healthcheck",
		Short:  "The container's health: waiting for setup, or the web app answering",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), healthTimeout)
			defer cancel()
			msg, ok := checkHealth(ctx, config.Path(), http.DefaultClient)
			fmt.Fprintln(cmd.OutOrStdout(), msg)
			if !ok {
				os.Exit(1)
			}
			return nil
		},
	}
}

// checkHealth says whether the machine of cfgPath is healthy, and why.
func checkHealth(ctx context.Context, cfgPath string, client *http.Client) (string, bool) {
	cfg, found, err := config.LoadFrom(cfgPath)
	if err != nil {
		return "server.toml cannot be read: " + err.Error(), false
	}
	if !found {
		return "waiting for setup (no " + cfgPath + ")", true
	}
	_, port, err := net.SplitHostPort(strings.TrimSpace(cfg.Server.Listen))
	if err != nil || port == "" {
		return fmt.Sprintf("server.toml's listen %q names no port", cfg.Server.Listen), false
	}
	url := "http://127.0.0.1:" + port + "/api/system/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err.Error(), false
	}
	withLocalToken(req, cfg)
	resp, err := noRedirect(client).Do(req)
	if err != nil {
		return url + ": " + err.Error(), false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("%s answered %d", url, resp.StatusCode), false
	}
	return url + " answered 200", true
}

// noRedirect is client following no redirect: a request carrying the local token must not
// carry it to wherever a 30x points (Go copies custom headers to the next host).
func noRedirect(client *http.Client) *http.Client {
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// withLocalToken sends this machine's per-boot local token (the supervisor's, when this
// process can read it) on a request to its own API: the API's limits for clients then let
// it by, and it draws on the home's own budget (the API's middleware.InternalCallers), so
// a busy home does not refuse its own health and version checks. No token, no header.
func withLocalToken(req *http.Request, cfg config.Config) {
	b, err := os.ReadFile(supervisor.LocalTokenPath(cfg))
	if tok := strings.TrimSpace(string(b)); err == nil && tok != "" {
		req.Header.Set("X-Maktaba-Local-Token", tok)
	}
}
