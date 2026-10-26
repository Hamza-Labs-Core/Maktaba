package router

import (
	"database/sql"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/serversettings"
)

// MountServerSettings wires the admin's server.toml settings and restart (Epic 45
// story 45.3) through the supervisor this API runs under (none: unavailable).
func MountServerSettings(r chi.Router, db *sql.DB) {
	(&serversettings.Handler{Supervisor: serversettings.SupervisorFromEnv(), DB: db}).Mount(r)
}
