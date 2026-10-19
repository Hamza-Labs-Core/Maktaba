package system

import (
	"encoding/json"
	"net/http"
	"runtime"
	"sync/atomic"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
)

// VersionInfo is the response shape for GET /api/system/version
// (Story 7.20 AC-2). Distinct from the package-level `BuildVersion`
// constant — the response carries a few derived fields the constants
// can't.
type VersionInfo struct {
	Version   string `json:"version"`
	BuildSHA  string `json:"build_sha"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
	SchemaRev int    `json:"schema_revision"`
	// Channel is the update channel this build tracks (stable|beta),
	// derived from the version string or the MAKTABA_UPDATE_CHANNEL
	// override (Story 28.1). Additive field — older clients ignore it.
	Channel string `json:"channel"`
	// Commit mirrors BuildSHA under the field name the auto-update UI
	// (Epic 28) and version.json expect; both are the same git SHA.
	Commit string `json:"commit"`
	// BuildDate mirrors BuildTime under the auto-update field name.
	BuildDate string `json:"build_date"`
	// Epic 35 story 35.11 AC7: which machine of a cluster answered — "home" or
	// "node" — its node id (nodes), the home's instance id and this machine's own
	// web address, so a typed node address is recognised as part of its cluster.
	Role           string `json:"role"`
	NodeID         string `json:"node_id,omitempty"`
	HomeInstanceID string `json:"home_instance_id,omitempty"`
	WebURL         string `json:"web_url,omitempty"`
}

// Machine is what /api/system/version says about the machine (SetMachine).
type Machine struct {
	Role           string
	NodeID         string
	HomeInstanceID string
	WebURL         string
}

var machine atomic.Pointer[Machine]

// SetMachine records this machine's place in the cluster (main.go; the home learns its
// instance id after the identity bootstrap).
func SetMachine(m Machine) { machine.Store(&m) }

// CurrentMachine is the recorded machine (role "home" until told otherwise).
func CurrentMachine() Machine {
	if m := machine.Load(); m != nil {
		return *m
	}
	return Machine{Role: "home"}
}

// VersionHandler returns a handler for GET /api/system/version. The
// schemaRev argument carries the binary's expected schema revision
// (Story 22.4 emits this constant from the migrations manifest); a
// running schema check belongs in /api/system/health, not here.
func VersionHandler(schemaRev int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		info := VersionInfo{
			Version:   version.Version,
			BuildSHA:  version.Commit,
			BuildTime: version.BuildDate,
			GoVersion: runtime.Version(),
			SchemaRev: schemaRev,
			Channel:   version.Channel(),
			Commit:    version.Commit,
			BuildDate: version.BuildDate,
		}
		m := CurrentMachine()
		info.Role, info.NodeID, info.HomeInstanceID, info.WebURL = m.Role, m.NodeID, m.HomeInstanceID, m.WebURL
		if info.Role == "" {
			info.Role = "home"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(info)
	})
}
