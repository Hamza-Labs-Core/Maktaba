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
}

// VersionHandler returns a handler for GET /api/system/version. The
// schemaRev argument carries the binary's expected schema revision
// (Story 22.4 emits this constant from the migrations manifest); a
// running schema check belongs in /api/system/health, not here.
func VersionHandler(schemaRev int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := VersionInfo{
			Version:   version.Version,
			BuildSHA:  version.Commit,
			BuildTime: version.BuildDate,
			GoVersion: runtime.Version(),
			SchemaRev: schemaRev,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(info)
	})
}
