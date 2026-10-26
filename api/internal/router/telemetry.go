// Epic 16 (stories 16.5, 16.7) handler wiring: the opt-in telemetry sink
// (POST /api/telemetry, POST /api/telemetry/web-vitals, DELETE
// /api/telemetry/devices/{pseudonym}). Kept in its own file, as p29.go.
//
// The routes are on the auth gate's public allowlist: a device posts under its
// pseudonym, never as a user. The handler rate-limits per IP and stores no IP.
// The retention sweeper is started by main.go, as the watch reaper is.
package router

import (
	"database/sql"
	"log/slog"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/telemetry"
)

// TelemetryDeps bundles the sink's dependencies.
type TelemetryDeps struct {
	DB *sql.DB
	// Enabled false is the server's opt-out: the POSTs store nothing.
	Enabled bool
	Logger  *slog.Logger
}

// MountTelemetry attaches the sink's routes onto r. Safe to skip when DB is nil.
func MountTelemetry(r chi.Router, d TelemetryDeps) {
	if d.DB == nil {
		return
	}
	telemetry.New(d.DB, d.Enabled, d.Logger).Mount(r)
}

// TelemetryEnabled reads MAKTABA_TELEMETRY_ENABLED, the supervisor's form of
// server.toml's [telemetry] enabled: on unless it says off/false/no/0. Clients
// send nothing until their user opts in, whatever this says.
func TelemetryEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MAKTABA_TELEMETRY_ENABLED"))) {
	case "off", "false", "no", "0":
		return false
	}
	return true
}
