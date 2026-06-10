// Command maktaba-cloud is the single-binary entry point for the
// Maktaba cloud service. Roles are selected with `--role api|relay|worker`.
//
// The subcommand surface mirrors the on-prem `api` binary so operator
// muscle memory carries over:
//
//	maktaba-cloud serve --role api      # default
//	maktaba-cloud migrate up
//	maktaba-cloud migrate down 1
//	maktaba-cloud migrate status
//	maktaba-cloud version
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// Version metadata baked in via -ldflags at build time. Falls back to
// "dev" / "unknown" when running from `go run`.
var (
	Version = "dev"
	Commit  = "unknown"
	BuiltAt = ""
)

func main() {
	if len(os.Args) < 2 {
		runServe(os.Args[1:])
		return
	}
	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "migrate":
		runMigrate(os.Args[2:])
	case "version":
		runVersion()
	default:
		runServe(os.Args[1:])
	}
}

func runVersion() {
	fmt.Fprintf(os.Stdout, "maktaba-cloud %s\n  commit  %s\n  built   %s\n  go      %s\n", Version, Commit, BuiltAt, runtime.Version())
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	role := fs.String("role", "", "process role: api|relay|worker (required)")
	cfgPath := fs.String("config", "", "path to cloud.toml")
	_ = fs.Parse(args)

	if *role == "" {
		fmt.Fprintln(os.Stderr, "--role required (api|relay|worker)")
		os.Exit(2)
	}

	logger := newLogger()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(2)
	}
	if err := config.Validate(cfg, config.Role(*role)); err != nil {
		logger.Error("config invalid", "role", *role, "err", err)
		os.Exit(2)
	}

	pool, err := db.Open(cfg.Database.URL, cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns, cfg.Database.ConnMaxLifetime)
	if err != nil {
		logger.Error("db open failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	mig := db.NewMigrator(pool, migrations.FS, ".")
	if err := mig.Up(context.Background()); err != nil {
		logger.Error("migrate up failed", "err", err)
		os.Exit(1)
	}

	build := server.BuildInfo{
		Version: Version, Commit: Commit, BuiltAt: BuiltAt,
		Role: *role, GoVer: runtime.Version(),
	}

	switch *role {
	case "api":
		runAPI(logger, cfg, pool, mig, build)
	case "relay":
		runRelay(logger, cfg, pool, mig, build)
	case "worker":
		runWorker(logger, cfg, pool, mig, build)
	default:
		logger.Error("unknown role", "role", *role)
		os.Exit(2)
	}
}

func runMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cloud.toml")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	sub := "up"
	rest := fs.Args()
	if len(rest) > 0 {
		sub = rest[0]
	}
	logger := newLogger()
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("config load", "err", err)
		os.Exit(2)
	}
	pool, err := db.Open(cfg.Database.URL, cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns, cfg.Database.ConnMaxLifetime)
	if err != nil {
		logger.Error("db open", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	mig := db.NewMigrator(pool, migrations.FS, ".")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	switch strings.ToLower(sub) {
	case "up":
		if err := mig.Up(ctx); err != nil {
			logger.Error("migrate up", "err", err)
			os.Exit(1)
		}
	case "down":
		steps := 1
		if len(rest) > 1 {
			_, _ = fmt.Sscanf(rest[1], "%d", &steps)
		}
		if err := mig.Down(ctx, steps); err != nil {
			logger.Error("migrate down", "err", err)
			os.Exit(1)
		}
	case "status":
		ok, err := mig.AtHead(ctx)
		if err != nil {
			logger.Error("migrate status", "err", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "at_head:", ok)
	default:
		fmt.Fprintln(os.Stderr, "unknown migrate subcommand:", sub)
		os.Exit(2)
	}
}

func newLogger() *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h)
}

// httpServer is a small helper to wire chi + timeouts consistently
// across roles.
func httpServer(addr string, handler http.Handler, readTO, writeTO time.Duration) *http.Server {
	return &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  readTO,
		WriteTimeout: writeTO,
	}
}
