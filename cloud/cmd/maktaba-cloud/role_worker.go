package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
)

// runWorker is the background-job role. Workers do not serve user
// traffic — they only host the control port at 127.0.0.1:9090 for the
// LB health checks, and run periodic tasks (bandwidth roll-ups,
// expired-token cleanup, account-deletion purges).
func runWorker(logger *slog.Logger, cfg config.Config, pool *db.Pool, _ *db.Migrator, _ server.BuildInfo) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","role":"worker"}`))
	})
	srv := &http.Server{Addr: "127.0.0.1:9090", Handler: mux, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	go func() { _ = srv.ListenAndServe() }()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go cleanupExpired(ctx, logger, pool)
	go purgeDeletedAccounts(ctx, logger, pool)

	// Block until SIGTERM.
	if err := server.Run(ctx, logger, srv, cfg.Server.ShutdownGrace); err != nil {
		logger.Error("worker exited", "err", err)
		os.Exit(1)
	}
}

// cleanupExpired sweeps stale claim tokens and revoked sessions. Runs
// every 10 minutes; the DELETE is cheap because both tables are
// indexed on expires_at.
func cleanupExpired(ctx context.Context, logger *slog.Logger, pool *db.Pool) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := pool.DB.ExecContext(ctx, `DELETE FROM server_claims WHERE expires_at < now() - INTERVAL '1 hour'`); err != nil {
				logger.Warn("worker: claim cleanup", "err", err)
			}
			if _, err := pool.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < now() - INTERVAL '1 day'`); err != nil {
				logger.Warn("worker: session cleanup", "err", err)
			}
		}
	}
}

// purgeDeletedAccounts removes users whose deletion hold has elapsed.
// We run once an hour. CASCADE on the FK chains drops sessions,
// oauth links, servers, etc., automatically.
func purgeDeletedAccounts(ctx context.Context, logger *slog.Logger, pool *db.Pool) {
	t := time.NewTicker(1 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := pool.DB.ExecContext(ctx, `
                DELETE FROM users WHERE id IN (
                    SELECT user_id FROM account_deletions
                    WHERE cancelled_at IS NULL AND purge_after < now()
                )
            `); err != nil {
				logger.Warn("worker: deletion purge", "err", err)
			}
		}
	}
}
