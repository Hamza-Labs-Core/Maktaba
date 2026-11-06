package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// serveStatus is this serve's status board (Epic 53 story 53.03), once the supervisor
// exists; before that /status.json says every group is starting.
var serveStatus atomic.Pointer[supervisor.StatusBoard]

// serveStatusJSON is GET /status.json on the web port.
func serveStatusJSON(w http.ResponseWriter, r *http.Request) {
	if b := serveStatus.Load(); b != nil {
		b.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(supervisor.StartingStatus(time.Now()))
}

// startStatusBoard computes /status.json from the supervisor's processes, the
// reconciler's phases (off is not down) and, on a home, a SELECT 1 over a connection of
// its own every 10 s (a Postgres that runs but refuses connections).
func startStatusBoard(ctx context.Context, cfg config.Config, sup *supervisor.Supervisor, rec *supervisor.Reconciler, node *nodeServices) {
	b := supervisor.NewStatusBoard(sup)
	if rec != nil {
		b.Phases = rec.Phases
	}
	if machine, dsn, ok := componentsTarget(cfg); ok && node == nil && machine != "" {
		if db, err := sql.Open("postgres", dsn); err == nil {
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(1)
			db.SetConnMaxIdleTime(time.Minute)
			b.DBCheck = func(ctx context.Context) error {
				var one int
				return db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
			}
			go func() { <-ctx.Done(); _ = db.Close() }()
		}
	}
	serveStatus.Store(b)
	go b.Run(ctx)
}
