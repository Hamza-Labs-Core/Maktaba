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
//	maktaba-cloud migrate applied
//	maktaba-cloud migrate undo 500025 500026   (the Down scripts of these, when they lose nothing)
//	maktaba-cloud staff add|grant|revoke|disable|enable|reset-mfa|unlock|list   # console staff (50.33)
//	maktaba-cloud license issue|pubkey|revoke|unrevoke  # Epic 16 licenses
//	maktaba-cloud version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/obs"
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
	case "staff":
		runStaff(os.Args[2:])
	case "admin":
		runAdmin(os.Args[2:])
	case "relay":
		runRelayCmd(os.Args[2:])
	case "relay-dns":
		// the witness's round robin over the relay pool (story 50.16)
		runRelayDNS(os.Args[2:])
	case "license":
		runLicense(os.Args[2:])
	case "logs":
		// W's role (53.17): every node's lines, kept and indexed
		runLogs(os.Args[2:])
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
	role := fs.String("role", "", "process role: api|relay|worker|dns (required)")
	cfgPath := fs.String("config", "", "path to cloud.toml")
	_ = fs.Parse(args)

	if *role == "" {
		fmt.Fprintln(os.Stderr, "--role required (api|relay|worker|dns)")
		os.Exit(2)
	}

	logger := newLogger(*role)
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(2)
	}
	if err := config.Validate(cfg, config.Role(*role)); err != nil {
		logger.Error("config invalid", "role", *role, "err", err)
		os.Exit(2)
	}
	for _, w := range config.Warnings(cfg) {
		logger.Warn("config", "warning", w)
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
	releaseStartupMemory() // goose's pooled parse buffers (story 50.14's heap per tunnel)

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
	case "dns":
		runDNS(logger, cfg, pool, mig, build)
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
	logger := newLogger("migrate")
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
	case "applied":
		vs, err := mig.Applied(ctx)
		if err != nil {
			logger.Error("migrate applied", "err", err)
			os.Exit(1)
		}
		for _, v := range vs {
			fmt.Fprintln(os.Stdout, v)
		}
	case "undo":
		// what a failed deploy applied (deploy/cloud-relay/ops/rollout.sh reads the exit codes)
		vs, err := parseVersions(rest[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := mig.Undo(ctx, vs); err != nil {
			logger.Error("migrate undo", "err", err)
			os.Exit(undoExit(err))
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

// parseVersions reads migrate undo's versions.
func parseVersions(args []string) ([]int64, error) {
	if len(args) == 0 {
		return nil, errors.New("migrate undo: name the versions")
	}
	var out []int64
	for _, a := range args {
		v, err := strconv.ParseInt(a, 10, 64)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("migrate undo: not a version: %q", a)
		}
		out = append(out, v)
	}
	return out, nil
}

// undoExit is migrate undo's exit code: 3, nothing changed and the schema is better kept (a Down
// script would delete rows written since); 4, nothing changed and nothing can undo it; 1, it
// failed, part way perhaps.
func undoExit(err error) int {
	switch {
	case errors.Is(err, db.ErrUndoKeeps):
		return 3
	case errors.Is(err, db.ErrUndoLossy):
		return 4
	default:
		return 1
	}
}

// newLogger is the shared logger (shared/log/go, Epic 53 story 53.01): every line
// carries service (cloud.<role>), version, env and node like the home's, with the
// shared redaction. MAKTABA_ENV (default prod) and MAKTABA_NODE_ID (the cloud node)
// name the rest.
// Its lines are counted for /metrics (53.17 §4: maktaba_log_lines_total, and error lines by
// code in maktaba_errors_total), the service named for the request series too.
func newLogger(role string) *slog.Logger {
	opts := logOptions(role)
	obs.SetService(opts.Service)
	// this node's keeper and the forwarding to W (53.17 §2), when the environment sets them;
	// W's own role keeps no second copy in the folder it serves
	if role != "logs" {
		node := opts.Node
		if node == "" {
			node, _ = os.Hostname()
		}
		if tee := logPath(opts.Service, node); tee != nil {
			opts.Output = io.MultiWriter(os.Stdout, tee)
		}
	}
	l := slog.New(obs.Handler(mlog.Init(opts).Handler(), opts.Service, obs.Default))
	slog.SetDefault(l)
	return l
}

func logOptions(role string) mlog.Options {
	env := os.Getenv("MAKTABA_ENV")
	if env == "" {
		env = "prod"
	}
	return mlog.Options{
		Service:      "cloud." + role,
		Env:          env,
		Version:      Version,
		Node:         os.Getenv("MAKTABA_NODE_ID"),
		Format:       "json",
		RingCapacity: -1,
	}
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
