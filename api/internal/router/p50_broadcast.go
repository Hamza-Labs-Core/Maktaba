// Epic 50 story 50.22 wiring: the Broadcast page's routes (/api/tv/broadcast) and the
// livetv.broadcast loop that keeps the published channels' ingest tokens fresh.
package router

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
)

// BroadcastDeps are the broadcast surface's dependencies.
type BroadcastDeps struct {
	DB *sql.DB
	// AgentAddr is the relay agent's loopback admin address (MAKTABA_CLOUDLINK_ADMIN);
	// "": the home is not claimed in Maktaba Cloud, so nothing can be published.
	AgentAddr string
	Loops     *runner.Runner
	Logger    *slog.Logger
	// NoLoop: do not start the token loop (tests).
	NoLoop bool
}

// MountBroadcast mounts /api/tv/broadcast and declares the api:broadcast_tokens loop
// (component livetv.broadcast: it stops while broadcasting is off).
func MountBroadcast(r chi.Router, d BroadcastDeps) *broadcast.Service {
	svc := &broadcast.Service{Store: &broadcast.Store{DB: d.DB}, Logger: d.Logger}
	if d.AgentAddr != "" {
		svc.Cloud = &broadcast.Agent{Addr: d.AgentAddr}
	}
	if d.DB != nil && !d.NoLoop && svc.Cloud != nil {
		d.Loops.Declare("broadcast_tokens", runner.Func(svc.Run))
	}
	(&broadcast.Routes{Service: svc, Names: userNames(d.DB)}).Mount(r)
	return svc
}

// userNames is a user's display name (else username) for the rights confirmation.
func userNames(db *sql.DB) func(ctx context.Context, id string) string {
	return func(ctx context.Context, id string) string {
		if db == nil {
			return ""
		}
		var name string
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(display_name, ''), username) FROM users WHERE id::text = $1`,
			id).Scan(&name); err != nil {
			return ""
		}
		return strings.TrimSpace(name)
	}
}
