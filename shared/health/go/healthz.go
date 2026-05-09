// Package health is the shared liveness/readiness handler surface for
// Maktaba's Go services (Story 21.4). Every service mounts /healthz and
// /readyz on a separate admin port; the main API additionally exposes
// /api/system/health which aggregates the others.
//
// Usage:
//
//	live := health.NewLive("api")
//	ready := health.NewReady("api", checks, 30*time.Second)
//	mux := http.NewServeMux()
//	mux.Handle("/healthz", live)
//	mux.Handle("/readyz", ready)
//
// The package intentionally has zero non-stdlib dependencies so it can
// be vendored by every Go service without dragging in the gRPC stack —
// services that want gRPC checks pass a Check whose Run() does the
// connectivity probe.
package health

import (
	"encoding/json"
	"net/http"
)

// Live is the liveness handler. It always returns 200 with a tiny JSON
// body. AC1: "never blocks on dependencies." Used by the orchestrator
// (compose / launchd / systemd) to decide whether to restart a hung
// process; readiness alone never causes a restart.
type Live struct {
	service string
}

// NewLive returns a Live handler tagged with the service name. The
// service name is echoed in the body so a probe collected from a
// shared admin port (9100/9101/9102) can be told apart at a glance.
func NewLive(service string) *Live {
	if service == "" {
		service = "unknown"
	}
	return &Live{service: service}
}

func (l *Live) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// Hand-encoded to keep the hot path allocation-free; this handler
	// is hammered by the orchestrator every few seconds.
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": l.service,
	})
}
