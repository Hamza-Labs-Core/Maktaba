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
	resp, err := client.Do(req)
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
