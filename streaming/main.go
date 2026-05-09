// Package main is the entry point for the Maktaba streaming server.
//
// Stub created by Story 22.1; Story 22.3 added the `serve` subcommand
// so compose has a long-lived process to attach a healthcheck to;
// Story 21.1 wired the structured logger; Story 21.4 split serve into
// a public mux (port 8081, real HLS routes land with Epic 08) and an
// admin mux (port 9101, /healthz + /readyz).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	health "github.com/Hamza-Labs-Core/Maktaba/shared/health/go"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--version":
			fmt.Fprintln(os.Stdout, version.String())
			return
		case "serve":
			if err := runServe(); err != nil {
				fmt.Fprintf(os.Stderr, "serve: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	logger := initLogger()
	logger.Info("starting maktaba-streaming",
		"commit", version.Commit,
		"build_date", version.BuildDate,
		"event", "startup",
	)
	logger.Info("stub server: pass `serve` to launch the placeholder HTTP server")
}

// initLogger configures the global structured logger from environment.
// Idempotent: subsequent calls return the cached instance.
func initLogger() *slog.Logger {
	return mlog.Init(mlog.Options{
		Service: "streaming",
		Env:     env(),
		Version: version.Version,
	})
}

func env() string {
	if v := os.Getenv("MAKTABA_ENV"); v != "" {
		return v
	}
	return "dev"
}

// runServe stands up two HTTP servers (mirrors api/main.go):
//
//   - Public on $MAKTABA_HTTP_ADDR (default :8081). Today serves only
//     a placeholder; Epic 08 lands the HLS routes.
//   - Admin on $MAKTABA_ADMIN_ADDR (default :9101) carrying /healthz
//     and /readyz for the orchestrator. Plan §0 isolates the probe
//     port so a hot HLS handler can't starve liveness.
func runServe() error {
	logger := initLogger()

	publicAddr := os.Getenv("MAKTABA_HTTP_ADDR")
	if publicAddr == "" {
		publicAddr = ":8081"
	}
	adminAddr := os.Getenv("MAKTABA_ADMIN_ADDR")
	if adminAddr == "" {
		adminAddr = ":9101"
	}

	checks := buildChecks()
	warm := warmPeriod()
	adminMux := health.AdminMux("streaming", checks, warm)

	publicMux := http.NewServeMux()
	// Placeholder until Epic 08 wires HLS. Liveness is forwarded to
	// the public port too so callers that only know about :8081 can
	// still get a 200.
	publicMux.Handle("/healthz", health.NewLive("streaming"))

	publicSrv := &http.Server{
		Addr:              publicAddr,
		Handler:           publicMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	adminSrv := &http.Server{
		Addr:              adminAddr,
		Handler:           adminMux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 2)
	go func() {
		logger.Info("listening", "addr", publicAddr, "kind", "public", "event", "http_listen")
		errCh <- publicSrv.ListenAndServe()
	}()
	go func() {
		logger.Info("listening", "addr", adminAddr, "kind", "admin", "event", "http_listen")
		errCh <- adminSrv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down", "event", "http_shutdown")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		errPublic := publicSrv.Shutdown(shutdownCtx)
		errAdmin := adminSrv.Shutdown(shutdownCtx)
		return errors.Join(errPublic, errAdmin)
	case err := <-errCh:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = publicSrv.Shutdown(shutdownCtx)
		_ = adminSrv.Shutdown(shutdownCtx)
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

// buildChecks assembles the readiness check set for streaming. The
// service's only hard dependency in v1 is the API (for token-claim
// verification once Epic 08 lands); we model it via a TCP dial against
// $MAKTABA_API_URL's host. EC2 in the story documents the "all
// streaming replicas down" case from the *aggregator's* perspective —
// at the per-service level we just report whether the API is reachable.
func buildChecks() []health.Check {
	checks := []health.Check{}
	if peers := os.Getenv("MAKTABA_GRPC_PEERS"); peers != "" {
		for _, pair := range strings.Split(peers, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			name, addr, ok := strings.Cut(pair, "=")
			if !ok {
				addr = pair
				name = "peer"
			}
			checks = append(checks, &health.TCPDial{N: strings.TrimSpace(name), Addr: strings.TrimSpace(addr)})
		}
	}
	return checks
}

// warmPeriod is plan TC3's 30 s cold-start window. Override via
// $MAKTABA_HEALTH_WARM (e.g. "0s" in tests).
func warmPeriod() time.Duration {
	v := os.Getenv("MAKTABA_HEALTH_WARM")
	if v == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 30 * time.Second
	}
	return d
}
