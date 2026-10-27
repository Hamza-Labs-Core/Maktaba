package admin

import (
	"net/http"
	"strconv"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/telemetry"
)

// Usage is the anonymous usage-data view (Epic 16 story 16.7): installs,
// versions, platforms, apps and features in use, top error groups and
// web-vitals p75 over the last ?days= days (default 30, at most 400).
func (d *Deps) Usage(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	u, err := telemetry.QueryUsage(r.Context(), d.DB, d.now(), days)
	if err != nil {
		d.fail(w, r, "usage", err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}
