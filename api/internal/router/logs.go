package router

import (
	"database/sql"
	"time"

	"github.com/go-chi/chi/v5"

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
	}
	h.Mount(r)
	return h.Forwarder()
}
