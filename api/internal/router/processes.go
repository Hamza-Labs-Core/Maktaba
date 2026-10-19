package router

import (
	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/processes"
)

// ProcessesDeps wires the process-supervision surface (Story 22.9).
type ProcessesDeps struct {
	// SupervisorURL is $MAKTABA_SUPERVISOR_URL, set by maktaba-server.
	// Empty: GET /api/system/processes reports available=false.
	SupervisorURL string
}

// MountProcesses wires GET /api/system/processes (admin-only).
func MountProcesses(r chi.Router, d ProcessesDeps) {
	(&processes.Handler{SupervisorURL: d.SupervisorURL}).Mount(r)
}
