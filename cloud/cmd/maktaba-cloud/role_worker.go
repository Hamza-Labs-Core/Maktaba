package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	mailpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/obs"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/privacy"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/ratelimit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// workerControlAddr is the worker's control port, for the LB health checks. A var only so the
// child-process test can run the worker on a free port.
var workerControlAddr = "127.0.0.1:9090"

// runWorker is the background-job role. Workers do not serve user
// traffic — they only host the control port (workerControlAddr) for the
// LB health checks, and run periodic tasks (bandwidth roll-ups,
// expired-token cleanup, account-deletion purges).
//
// Every api node runs a worker (Epic 50 story 50.17 §2), and the jobs run only on the one
// holding the worker's leader lock (db.WorkerLockID): another node takes it within 15 s when
// the holder goes. worker_leader{node} on /metrics is 1 on the holder.
//
// server.Run is the only listener: a second ListenAndServe on the same
// address made the worker exit 1 at start (c15649f76 until this fix), so
// none of its jobs ever ran.
func runWorker(logger *slog.Logger, cfg config.Config, pool *db.Pool, _ *db.Migrator, _ server.BuildInfo) {
	leader, closeLock, err := workerLeader(cfg, pool, logger)
	if err != nil {
		logger.Error("worker: the leader lock's connection", "err", err)
		os.Exit(2)
	}
	defer closeLock()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","role":"worker"}`))
	})
	// the worker's own series (53.17 §4): its error and log lines
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_ = obs.WriteMetrics(w)
		_ = writeLeaderMetric(w, leader)
	})
	// in the api pool (story 50.17) the witness's Prometheus scrapes worker_leader over the
	// private network: MAKTABA_CLOUD_WORKER_CONTROL_ADDR puts the port on the node's private
	// address (never a public one or a wildcard)
	addr := workerControlAddr
	if v := os.Getenv("MAKTABA_CLOUD_WORKER_CONTROL_ADDR"); v != "" {
		if err := config.ValidateOriginListen(v); err != nil {
			logger.Error("worker: MAKTABA_CLOUD_WORKER_CONTROL_ADDR", "addr", v, "err", err)
			os.Exit(2)
		}
		addr = v
	}
	srv := &http.Server{Addr: addr, Handler: mux, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go leader.Run(ctx, func(ctx context.Context) { workerJobs(ctx, logger, cfg, pool) })

	// Block until SIGTERM.
	if err := server.Run(ctx, logger, srv, cfg.Server.ShutdownGrace); err != nil {
		logger.Error("worker exited", "err", err)
		os.Exit(1)
	}
}

// workerJobs runs the worker's periodic jobs until ctx ends (the node stopped leading, or it
// stops). Each is idempotent: a node that loses the lock part way leaves its job for the next
// holder to run again.
func workerJobs(ctx context.Context, logger *slog.Logger, cfg config.Config, pool *db.Pool) {
	var wg sync.WaitGroup
	run := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}
	run(func() { cleanupExpired(ctx, logger, pool) })
	run(func() { purgeDeletedAccounts(ctx, logger, pool) })
	// story 50.19: relayed streams silent for 2 minutes stop counting against their plan
	run(func() { sweepStreams(ctx, logger, &billingpkg.Streams{DB: pool.DB}, time.Minute) })
	// story 50.19 §6: the hourly pay-as-you-go usage reports, with a payment provider that
	// takes payments
	if r := usageReporter(cfg, pool, logger); r != nil {
		run(func() { r.Run(ctx, time.Hour) })
	}
	// direct remote access (story 50.15): certificates and the outside checks
	if j := directJobs(cfg, pool, logger); j != nil {
		run(func() { j.Run(ctx) })
	}
	wg.Wait()
}

// workerLeader is the worker's leader lock, on database.direct_url when set (a session lock
// can't go through PgBouncer's transaction pool, story 50.18), else on the pool. The node's
// name is MAKTABA_NODE_ID, or the host's name.
func workerLeader(cfg config.Config, pool *db.Pool, logger *slog.Logger) (*db.Leader, func(), error) {
	node := os.Getenv("MAKTABA_NODE_ID")
	if node == "" {
		node, _ = os.Hostname()
	}
	l := &db.Leader{DB: pool.DB, LockID: db.WorkerLockID, Node: node, Logger: logger}
	if cfg.Database.DirectURL == "" {
		return l, func() {}, nil
	}
	direct, err := db.Open(cfg.Database.DirectURL, 2, 1, 0)
	if err != nil {
		return nil, nil, err
	}
	l.DB = direct.DB
	return l, func() { _ = direct.Close() }, nil
}

// writeLeaderMetric writes worker_leader{node}: 1 while this node runs the jobs. The alert
// fires when no node reports 1 for 2 minutes (deploy/cloud-ha/monitoring).
func writeLeaderMetric(w io.Writer, l *db.Leader) error {
	v := 0
	if l.Leading() {
		v = 1
	}
	_, err := fmt.Fprintf(w, "# HELP worker_leader 1 on the node that runs the worker's jobs (story 50.17).\n"+
		"# TYPE worker_leader gauge\nworker_leader{node=%q} %d\n", l.Node, v)
	return err
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
			// the shared rate buckets (story 50.17 §3) idle for a day have refilled long ago
			if _, err := ratelimit.SweepPG(ctx, pool.DB, 24*time.Hour); err != nil {
				logger.Warn("worker: rate bucket cleanup", "err", err)
			}
		}
	}
}

// purgeDeletedAccounts removes users whose deletion hold has elapsed.
// We run once an hour. Each account first goes through the GDPR erasure
// (privacy.DataSubjectService, story 30.2) so its push logs, which have
// no FK to users, go with it; CASCADE on the FK chains then drops
// sessions, oauth links, servers, etc.
func purgeDeletedAccounts(ctx context.Context, logger *slog.Logger, pool *db.Pool) {
	purger := privacy.NewAccountPurger(pool.DB)
	t := time.NewTicker(1 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			res, err := purger.PurgeDue(ctx)
			if err != nil {
				logger.Warn("worker: deletion purge", "err", err)
				continue
			}
			for id, ferr := range res.Failed {
				logger.Warn("worker: deletion purge left an account for the next pass", "user_id", id, "err", ferr)
			}
			if res.Purged > 0 {
				logger.Info("worker: purged deleted accounts", "accounts", res.Purged, "push_log_rows", res.PushRows)
			}
		}
	}
}

// sweepStreams deletes the relayed streams silent for billing.StreamSilence, every interval
// (story 50.19 §3: a stream that ended or a relay that crashed frees its slot).
func sweepStreams(ctx context.Context, logger *slog.Logger, st interface {
	Sweep(context.Context) (int64, error)
}, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := st.Sweep(ctx); err != nil {
				logger.Warn("worker: relayed streams sweep", "err", err)
			} else if n > 0 {
				logger.Info("worker: relayed streams ended", "streams", n, "event", "relay_streams_swept")
			}
		}
	}
}

// usageReporter is the worker's pay-as-you-go reporter, or nil when no payment provider takes
// payments (nothing can be billed). Its notices go to the account's email.
func usageReporter(cfg config.Config, pool *db.Pool, logger *slog.Logger) *billingpkg.Reporter {
	p, err := billingpkg.New(cfg.Billing.Provider)
	if err != nil || !p.Enabled() {
		return nil
	}
	users := stores.NewUsers(pool.DB)
	var mailer mailpkg.Mailer = mailpkg.LogMailer{Logger: logger}
	return &billingpkg.Reporter{DB: pool.DB, Provider: p, Logger: logger,
		Notify: func(ctx context.Context, uid string, near bool) error {
			u, err := users.ByID(ctx, uid)
			if err != nil {
				return err
			}
			kind, subject, body := "payg_cap_reached", "Your pay-as-you-go limit is reached",
				"You've reached your pay-as-you-go limit. Remote play continues at Basic quality until next month."
			if near {
				kind, subject, body = "payg_near_cap", "80% of your pay-as-you-go limit is used",
					"You've used 80% of your pay-as-you-go limit this month."
			}
			return mailer.Send(ctx, mailpkg.Message{To: u.Email, Kind: kind, Subject: subject, Body: body})
		}}
}
