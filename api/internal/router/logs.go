package router

import (
	"context"
	"database/sql"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	clusterh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/logs"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/system"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// LogsDeps bundles what the diagnostics-export surface needs. Every
// field is optional — the handler degrades to a smaller bundle rather
// than failing when a dependency is unwired (dev / no-DB stub).
type LogsDeps struct {
	// DB backs the connection-stats + active-job sections.
	DB *sql.DB
	// DataDir is the volume whose free space is reported.
	DataDir string
	// SchemaRev is the binary's expected schema revision.
	SchemaRev int
	// StartTime stamps process uptime.
	StartTime time.Time
	// AggregatorServices / StatsDB feed the health-check snapshot — the
	// same inputs the /api/system/health route uses.
	AggregatorServices []system.Service
	StatsDB            *sql.DB
	// Peers are downstream services whose /logs/recent the API proxies
	// into the bundle (streaming, pipeline).
	Peers []logs.PeerLog
	// Files reads a machine's own log files (LogFiles). nil: none are there.
	Files logs.FileSource
	// Switches is the home's components (the loops' runner): reports.share (story 53.16).
	Switches logs.Switches
}

// dashboardMachines is the cluster handler the dashboards' Overview asks which joined
// machines are offline (story 53.12): MountP31 sets it, after MountLogs ran.
var dashboardMachines atomic.Pointer[clusterh.Handler]

// offlineMachines are the joined machines that are offline now (none before the cluster
// is mounted, or on a home without one).
func offlineMachines(ctx context.Context) ([]string, error) {
	h := dashboardMachines.Load()
	if h == nil || h.Store == nil {
		return nil, nil
	}
	return h.OfflineMachines(ctx)
}

// machineNames are the machines' names as the Machines page shows them (none before the
// cluster is mounted, or on a home without one: the Overview then names each by its id).
func machineNames(ctx context.Context, ids []string) (map[string]string, error) {
	h := dashboardMachines.Load()
	if h == nil || h.Store == nil {
		return nil, nil
	}
	return h.MachineNames(ctx, ids)
}

// MountLogs wires the troubleshooting log-collection routes
// (/api/admin/logs/* and /api/diagnostics/export). The ring buffer is
// pulled from the process-global installed by mlog.Init. It answers the problem reports'
// forwarder to Maktaba (story 53.16), for the caller to declare as the
// api:reports_forwarder loop.
func MountLogs(r chi.Router, d LogsDeps) *logs.Forwarder {
	health := system.NewAggregatorWithStats(d.AggregatorServices, system.StatsConfig{
		DataDir: d.DataDir,
		DB:      d.StatsDB,
	})
	h := &logs.Handler{
		Ring:       mlog.Ring(),
		DB:         d.DB,
		DataDir:    d.DataDir,
		SchemaRev:  d.SchemaRev,
		StartTime:  d.StartTime,
		Health:     health,
		Peers:      d.Peers,
		FileSource: d.Files,
		Switches:   d.Switches,
		Offline:    offlineMachines,
		Names:      machineNames,
	}
	h.Mount(r)
	return h.Forwarder()
}
