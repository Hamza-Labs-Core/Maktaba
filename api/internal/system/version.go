package system

import (
	"encoding/json"
	"net/http"
	"runtime"

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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(info)
	})
}
