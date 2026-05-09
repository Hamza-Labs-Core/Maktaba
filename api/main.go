// Package main is the entry point for the Maktaba API server.
//
// Story 22.1 created the binary as a stub; Story 22.2 wired the
// `--version` flag and reproducibility envelope; Story 22.3 added the
// `serve` subcommand so compose has a long-lived process to attach a
// healthcheck to; Story 22.4 added the `migrate` subcommand. Story
// 07.x replaces the serve path with the real HTTP server.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
)

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--version":
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
		case "help", "-h", "--help":
			printUsage(os.Stdout)
			return
		}
	}
	fmt.Fprintf(os.Stdout, "maktaba-api %s: stub (Story 07 will replace this)\n", version.String())
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, `maktaba-api — Maktaba server binary

Usage:
  maktaba-api <command> [flags]

Commands:
  serve      Run the HTTP server (stub: only /healthz until Story 07)
  migrate    Run database migrations (see "migrate --help")
  --version  Print the binary's version
  help       Show this help`)
}

// runServe stands up a placeholder HTTP server so the compose
// container has a long-lived PID 1 with a /healthz endpoint for
// Story 21.4-shaped healthchecks. The real router lands with Epic 07.
func runServe() error {
	addr := os.Getenv("MAKTABA_HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","service":"api","version":%q}`+"\n", version.Version)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stdout, "maktaba-api %s: listening on %s (stub)\n", version.String(), addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}
