// Package metrics (handlers) exposes the operator-only relay-analytics
// dashboard and export (Epic 30, Stories 30.3/30.4) plus the Article 30
// processing-records endpoint (30.2). It mounts on the api role inside
// the authenticated group and reads the relay_metrics_* tables the relay
// role writes (README D8). Access is gated on the caller's email domain,
// mirroring handlers/admin.
package metrics

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	metricspkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/metrics"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/privacy"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

type Deps struct {
	DB            *sql.DB
	Users         *stores.Users
	AllowedDomain string
	Store         *metricspkg.Store
	// Live optionally returns the collector's last-sampled gauges when the
	// reader is co-located with the relay; nil falls back to the DB.
	Live func() (servers, tunnels int)
	// Deletion backs the right-to-erasure admin endpoint (Story 30.2).
	Deletion *privacy.DataSubjectService
}

// Mount wires the relay-analytics routes. The caller mounts this inside
// an authenticated chi group.
func Mount(r chi.Router, d Deps) {
	r.Get("/v1/admin/metrics/overview", d.RequireAdmin(d.Overview))
	r.Get("/v1/admin/metrics/bandwidth", d.RequireAdmin(d.Bandwidth))
	r.Get("/v1/admin/metrics/push", d.RequireAdmin(d.Push))
	r.Get("/v1/admin/metrics/subscriptions", d.RequireAdmin(d.Subscriptions))
	r.Get("/v1/admin/metrics/geo", d.RequireAdmin(d.Geo))
	r.Get("/v1/admin/metrics/export", d.RequireAdmin(d.Export))

	// Story 30.2 — Article 30 records (operator-gated) + erasure hook.
	r.Get("/v1/admin/privacy/processing-records", d.RequireAdmin(privacy.ProcessingRecordsHandler))
	if d.Deletion != nil {
		r.Delete("/v1/admin/privacy/users/{id}", d.RequireAdmin(d.DeleteUserData))
	}
}

// RequireAdmin gates a handler on the caller's email domain — identical
// rule to handlers/admin so the operator allow-list stays consistent.
func (d *Deps) RequireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.GetUserID(r.Context())
		u, err := d.Users.ByID(r.Context(), uid)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if domainOf(u.Email) != strings.ToLower(d.AllowedDomain) {
			writeErr(w, http.StatusForbidden, "forbidden")
			return
		}
		h.ServeHTTP(w, r)
	}
}

// Overview — live connection gauges + range totals.
func (d *Deps) Overview(w http.ResponseWriter, r *http.Request) {
	start, key := parseRange(r)
	totals, err := d.Store.CounterTotals(r.Context(), start)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed")
		return
	}
	servers, tunnels := d.live(r)
	totalServers, _ := d.Store.ServerCount(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"range":             key,
		"connected_servers": servers,
		"active_tunnels":    tunnels,
		"total_servers":     totalServers,
		"bandwidth_in":      totals[metricspkg.MetricBandwidthIn],
		"bandwidth_out":     totals[metricspkg.MetricBandwidthOut],
		"requests":          totals[metricspkg.MetricRequests],
	})
}

// Bandwidth — hourly in/out series for graphs.
func (d *Deps) Bandwidth(w http.ResponseWriter, r *http.Request) {
	start, key := parseRange(r)
	series, err := d.Store.Series(r.Context(),
		[]string{metricspkg.MetricBandwidthIn, metricspkg.MetricBandwidthOut}, start)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": key, "series": series})
}

// Push — delivery stats from push_dispatch_log.
func (d *Deps) Push(w http.ResponseWriter, r *http.Request) {
	start, key := parseRange(r)
	ps, err := d.Store.PushStats(r.Context(), start)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": key, "sent": ps.Sent, "failed": ps.Failed})
}

// Subscriptions — plan breakdown from users.
func (d *Deps) Subscriptions(w http.ResponseWriter, r *http.Request) {
	rows, err := d.Store.Subscriptions(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"breakdown": rows})
}

// Geo — request totals by country for the heatmap.
func (d *Deps) Geo(w http.ResponseWriter, r *http.Request) {
	start, key := parseRange(r)
	pts, err := d.Store.Geo(r.Context(), start)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": key, "countries": pts})
}

// Export — CSV/JSON dump of the hourly rollup (Story 30.4).
func (d *Deps) Export(w http.ResponseWriter, r *http.Request) {
	start, _ := parseRange(r)
	rows, err := d.Store.ExportRows(r.Context(), start)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query_failed")
		return
	}
	if strings.ToLower(r.URL.Query().Get("format")) == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="relay-metrics.csv"`)
		_ = metricspkg.RenderCSV(w, rows)
		return
	}
	// Default (and unknown formats) → JSON (Story 30.4 AC).
	w.Header().Set("Content-Type", "application/json")
	_ = metricspkg.RenderJSON(w, rows)
}

// DeleteUserData runs the GDPR erasure for an account (Story 30.2).
func (d *Deps) DeleteUserData(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rep, err := d.Deletion.Delete(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "delete_failed")
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (d *Deps) live(r *http.Request) (int, int) {
	if d.Live != nil {
		return d.Live()
	}
	return 0, 0
}

// ─── helpers ────────────────────────────────────────────────────────────

// parseRange maps the `range` query param to a start time and echoes the
// canonical key. Defaults to 7d; unknown values also fall back to 7d.
func parseRange(r *http.Request) (time.Time, string) {
	now := time.Now().UTC()
	switch r.URL.Query().Get("range") {
	case "today":
		return now.Truncate(24 * time.Hour), "today"
	case "30d":
		return now.AddDate(0, 0, -30), "30d"
	case "90d":
		return now.AddDate(0, 0, -90), "90d"
	case "7d", "":
		return now.AddDate(0, 0, -7), "7d"
	default:
		return now.AddDate(0, 0, -7), "7d"
	}
}

func domainOf(email string) string {
	i := strings.LastIndex(email, "@")
	if i < 0 {
		return ""
	}
	return strings.ToLower(email[i+1:])
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, kind string) {
	writeJSON(w, code, map[string]string{"error": kind})
}
