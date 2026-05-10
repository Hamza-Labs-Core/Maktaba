// Package main is the entry point for the Maktaba API server.
//
// Story 22.1 created the binary as a stub; Story 22.2 wired the
// `--version` flag and reproducibility envelope; Story 22.3 added the
// `serve` subcommand so compose has a long-lived process to attach a
// healthcheck to; Story 22.4 added the `migrate` subcommand; Story
// 21.1 wired the structured logger; Story 21.4 split serve into a
// public mux (port 8080, /api/system/health aggregator) and an admin
// mux (port 9100, /healthz + /readyz). Story 07.x replaces the public
// mux's placeholder route with the real HTTP server.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/system"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
	health "github.com/Hamza-Labs-Core/Maktaba/shared/health/go"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--version":
			// Plain stdout — `--version` is parsed by tooling, not
			// humans. Initialising the logger first would emit a startup
			// line and break that contract.
			fmt.Fprintln(os.Stdout, version.String())
			return
		case "migrate":
			if err := runMigrate(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
				os.Exit(1)
			}
			return
		case "serve":
			if err := runServe(); err != nil {
				fmt.Fprintf(os.Stderr, "serve: %v\n", err)
				os.Exit(1)
			}
			return
		case "adduser":
			if err := runAddUser(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "adduser: %v\n", err)
				os.Exit(1)
			}
			return
		case "keys":
			if err := runKeys(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "keys: %v\n", err)
				os.Exit(1)
			}
			return
		case "help", "-h", "--help":
			printUsage(os.Stdout)
			return
		}
	}

	logger := initLogger()
	logger.Info("starting maktaba-api",
		"commit", version.Commit,
		"build_date", version.BuildDate,
		"event", "startup",
	)
	logger.Info("stub server: pass `serve` to launch the placeholder HTTP server, or `migrate` to apply migrations")
}

// initLogger configures the global structured logger from environment.
// Idempotent: subsequent calls return the cached instance.
func initLogger() *slog.Logger {
	return mlog.Init(mlog.Options{
		Service: "api",
		Env:     env(),
		Version: version.Version,
	})
}

// env returns the deployment environment (prod/dev/test). Defaults to
// "dev" when MAKTABA_ENV is unset, so a developer running `go run .`
// gets human-readable text logs.
func env() string {
	if v := os.Getenv("MAKTABA_ENV"); v != "" {
		return v
	}
	return "dev"
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, `maktaba-api — Maktaba server binary

Usage:
  maktaba-api <command> [flags]

Commands:
  serve      Run the HTTP server (stub: only /healthz until Story 07)
  migrate    Run database migrations (see "migrate --help")
  adduser    Seed the first admin user (Story 10.1 AC-4)
  keys       Generate or rotate RS256 JWT keys (Story 10.6)
  --version  Print the binary's version
  help       Show this help`)
}

// runServe stands up two HTTP servers:
//
//   - The "public" server on $MAKTABA_HTTP_ADDR (default :8080) carries
//     the API's user-facing routes — currently only the
//     /api/system/health aggregator until Epic 07 lands the real
//     router.
//   - The "admin" server on $MAKTABA_ADMIN_ADDR (default :9100) carries
//     the orchestrator's probes — /healthz and /readyz. Plan §0:
//     bound to the admin port so a misbehaving public-port handler
//     can't take readiness down with it, and so the probes are
//     trivially firewallable.
//
// Both servers share a graceful-shutdown context so SIGINT / SIGTERM
// drains them together with a 10 s budget.
func runServe() error {
	logger := initLogger()

	publicAddr := os.Getenv("MAKTABA_HTTP_ADDR")
	if publicAddr == "" {
		publicAddr = ":8080"
	}
	adminAddr := os.Getenv("MAKTABA_ADMIN_ADDR")
	if adminAddr == "" {
		adminAddr = ":9100"
	}

	// Build the readiness checks. The DB check only runs when
	// $DATABASE_URL is set so `serve` still works in the stub-stage
	// integration tests that don't bring up Postgres. Once Story 19.x
	// owns config, this block moves into the config-driven wiring.
	checks := buildChecks(logger)

	warm := warmPeriod()
	adminMux := health.AdminMux("api", checks, warm)

	auth, err := initAuth(logger)
	if err != nil {
		return err
	}

	publicMux := http.NewServeMux()
	publicMux.Handle("/api/system/health", system.NewAggregator(buildAggregatorServices()))
	publicMux.Handle("/api/.well-known/jwks.json", &keys.JWKSHandler{Set: auth.keys})
	// Forward /healthz on the public port too — convenient for compose
	// stacks that haven't been told about the admin port yet. Liveness
	// is cheap; exposing it twice costs nothing.
	publicMux.Handle("/healthz", health.NewLive("api"))

	publicSrv := &http.Server{
		Addr:              publicAddr,
		Handler:           auth.applySecurity(publicMux),
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

	// Reaper for the rotation overlap window (Story 10.6 AC-4): once
	// `rotation_overlap_sec` has elapsed since the previous-key was
	// added, drop it. 60s tick is much finer than the 24h overlap so
	// the reaper is essentially free.
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				if reaped := auth.keys.ReapExpired(now); reaped {
					logger.Info("auth: reaped previous JWT key after overlap",
						"event", "auth_keys_reaped")
				}
			}
		}
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
		// Whichever server failed first — collect it, then ask the
		// other to shut down cleanly so we don't leak a goroutine.
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

// buildChecks assembles the readiness check set from the runtime
// environment. The list is deliberately bounded — every check costs a
// real round-trip every time the orchestrator polls, so we keep it to
// the dependencies that, if absent, mean the API genuinely cannot
// serve traffic.
//
// EC3 in the story: SQLite mode skips Postgres-specific checks. The
// stub serve path treats $DATABASE_URL as the on/off switch; Story
// 19.x owns the config driver-detection.
func buildChecks(logger *slog.Logger) []health.Check {
	checks := []health.Check{}

	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			logger.Warn("readiness: skipping db check, sql.Open failed",
				"err", err, "event", "readiness_check_skipped")
		} else {
			// 1 conn cap: readiness must not contend with the API's
			// real query pool. PingContext below the cap is enough to
			// satisfy AC2's "≥ 1 healthy conn".
			db.SetMaxOpenConns(1)
			checks = append(checks, &health.DBPing{DB: db})
		}
	}

	if peers := os.Getenv("MAKTABA_GRPC_PEERS"); peers != "" {
		// Comma-separated `name=host:port` pairs. Plan §3 sketches a
		// gRPC connectivity check; until the gRPC clients are wired
		// in (Story 09 et seq.) we use the lighter TCPDial check —
		// "can we open a socket to the peer" is a strict subset of
		// "can we serve gRPC against it" and false negatives there
		// would have to be a regression elsewhere.
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

// buildAggregatorServices reads the aggregator's fan-out target list
// from $MAKTABA_HEALTH_PEERS. Format: comma-separated `name=URL`
// pairs, e.g.
//
//	MAKTABA_HEALTH_PEERS="streaming=http://streaming:9101/readyz,pipeline=http://pipeline:9102/readyz"
//
// Empty list = no fan-out; the aggregator endpoint still responds with
// status=ok and an empty services map, which is the right answer for
// a single-binary dev run.
func buildAggregatorServices() []system.Service {
	raw := os.Getenv("MAKTABA_HEALTH_PEERS")
	if raw == "" {
		return nil
	}
	out := []system.Service{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, url, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		out = append(out, system.Service{
			Name: strings.TrimSpace(name),
			URL:  strings.TrimSpace(url),
		})
	}
	return out
}

// warmPeriod is plan TC3's cold-start window. Defaults to 30 s; the
// integration tests override it to 0 via $MAKTABA_HEALTH_WARM=0 so
// they don't have to sleep.
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
