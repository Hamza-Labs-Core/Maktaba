// Epic 16 (stories 16.5, 16.7) handler wiring: anonymous usage data to Maktaba
// Cloud (POST /api/telemetry/app, DELETE /api/telemetry/app/{installID}, GET
// /api/telemetry/server, POST /api/telemetry/server/forget). Kept in its own
// file, as p29.go.
//
// Every route needs a signed-in user (the auth gate); the server's are an
// admin's (the handler checks). The sender that delivers the outbox is
// returned for main.go to start, as the watch reaper is.
package router

import (
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/telemetry"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
)

// TelemetryDeps bundles the usage data's dependencies.
type TelemetryDeps struct {
	DB *sql.DB
	// Enabled false is server.toml's [telemetry] enabled = "off": nothing
	// leaves the home, its own data nor any app's.
	Enabled  bool
	Settings *runtimecfg.Reader
	Version  string
	Errors   *telemetry.ErrorCounts
	// CloudURL is Maktaba Cloud's API origin ("" when there is none).
	CloudURL     func() string
	RemoteAccess func() bool
	Logger       *slog.Logger
}

// MountTelemetry attaches the routes onto r and returns the sender to run (nil
// when DB is nil, or when [telemetry] is off: nothing is ever sent).
func MountTelemetry(r chi.Router, d TelemetryDeps) *telemetry.Sender {
	if d.DB == nil {
		return nil
	}
	store := telemetry.PGStore{DB: d.DB}
	state := telemetry.PGState{DB: d.DB, Settings: d.Settings}
	reporter := &telemetry.Reporter{DB: d.DB, Version: d.Version, RemoteAccess: d.RemoteAccess,
		Errors: d.Errors, Logger: d.Logger}
	var sender *telemetry.Sender
	if d.Enabled {
		sender = &telemetry.Sender{Store: store, State: state, Reporter: reporter, URL: d.CloudURL,
			Client: &http.Client{Timeout: 30 * time.Second}, Logger: d.Logger}
	}
	(&telemetry.Handler{DB: d.DB, Enabled: d.Enabled, Store: store, State: state,
		Reporter: reporter, Sender: sender, Logger: d.Logger}).Mount(r)
	return sender
}

// TelemetryEnabled reads MAKTABA_TELEMETRY_ENABLED, the supervisor's form of
// server.toml's [telemetry] enabled: on unless it says off/false/no/0. On only
// lets the consents count: the admin's switch for the server's data and each
// app's user for the app's, both off by default.
func TelemetryEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MAKTABA_TELEMETRY_ENABLED"))) {
	case "off", "false", "no", "0":
		return false
	}
	return true
}
