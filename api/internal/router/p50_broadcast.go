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
	// RevocationKeys is MAKTABA_BROADCAST_PUBKEYS: broadcast signing keys beyond the release's.
	RevocationKeys string
}

// MountBroadcast mounts /api/tv/broadcast and declares the api:broadcast_tokens loop
// (component livetv.broadcast: it stops while broadcasting is off).
func MountBroadcast(r chi.Router, d BroadcastDeps) *broadcast.Service {
	svc := &broadcast.Service{Store: &broadcast.Store{DB: d.DB}, Logger: d.Logger}
	if d.AgentAddr != "" {
		agent := &broadcast.Agent{Addr: d.AgentAddr}
		svc.Cloud = agent
		// takedowns and reports (50.24): Maktaba's signed instructions and this home's records
		svc.Moderation = agent
		// the revocation list (50.25): honoured once the release or MAKTABA_BROADCAST_PUBKEYS
		// holds a broadcast signing key
		if keys, err := broadcast.RevocationKeys(d.RevocationKeys); err != nil {
			slogOr(d.Logger).Warn("broadcast: the revocation keys are unreadable; the list is not honoured", "err", err)
		} else {
			svc.Revocations = &broadcast.Revocations{Store: svc.Store, Source: agent, Keys: keys, Logger: d.Logger}
		}
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

func slogOr(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.Default()
}
