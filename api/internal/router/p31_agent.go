// Epic 31 phase 2 wiring: the cluster channel's hub
// (specs/epics/31-multi-instance/design-agent.md). Kept in its own file
// next to p31.go.
//
//	GET /api/cluster/agent — public (allowlisted), per-IP limited: every
//	  process's sealed channel;
//	GET /api/cluster/ws, /api/cluster/files…, POST /api/cluster/processes/
//	  {session}/cmd, /api/cluster/llm/v1/* — authenticated, admin-gated
//	  in-handler where the design says so.
package router

import (
	"context"
	"database/sql"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	clusterh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/transcription"
)

// P31AgentDeps bundles the hub's dependencies.
type P31AgentDeps struct {
	DB     *sql.DB
	Driver string // "postgres" (default) or "sqlite"
	// Config carries the settings (agent.FromEnv) and the home key; the
	// data sources are filled in here.
	Config agent.Config
}

// MountP31Agent builds the hub, mounts its routes, and hooks it to the
// phase-1 handler (revoke / remove closes a machine's sessions; a stage
// switch or drain change reloads its stages). The caller runs hub.Run.
func MountP31Agent(r chi.Router, h *clusterh.Handler, d P31AgentDeps) *agent.Hub {
	cfg := d.Config
	cfg.Store = h.Store
	if cfg.Logger == nil {
		cfg.Logger = h.Logger
	}
	if d.DB != nil && d.Driver != "sqlite" {
		// the same queue snapshot the Processing page reads (the
		// transcription card's time left included)
		qh := &queues.Handler{DB: d.DB, StageETA: map[string]func(context.Context, time.Time) (*float64, error){
			"transcribe": (&transcription.Handler{DB: d.DB}).ETASeconds,
		}}
		cfg.Queues = qh.BuildSnapshot
		cfg.Workers = qh.WorkersView
		cfg.Library = agent.DBLibrary(d.DB, time.Now)
		if cfg.Locator != nil && cfg.Locator.Roots == nil {
			cfg.Locator.Roots = agent.DBLibraryRoots(d.DB)
		}
	}
	hub := agent.New(cfg)
	hub.Mount(r)
	h.OnRevoke = hub.RevokeNode
	h.OnNodeChanged = hub.NodeChanged
	return hub
}
