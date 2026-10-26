// Package metrics (handlers) exposes the operator-only relay-analytics
// dashboard and export (Epic 30, Stories 30.3/30.4) plus the Article 30
// processing-records endpoint (30.2). It mounts on the api role inside
// the authenticated group and reads the relay_metrics_* tables the relay
// role writes (README D8). Access is gated by Deps.Gate — in production
// the cloud service-admin role check, the same gate as handlers/admin.
package metrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	authmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/middleware"
	metricspkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/metrics"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/privacy"
)

type Deps struct {
	DB *sql.DB
	// Gate admits service admins only (auth/middleware.RequireServiceAdmin).
	// A nil Gate refuses everyone: the routes fail closed.
	Gate func(http.Handler) http.Handler
	// Audit records erasures. Optional.
	Audit *audit.Log
	Store *metricspkg.Store
	// Live returns the live connection gauges: the collector's when the
	// reader shares a process with the relay, else the relay's newest
	// flushed sample (LiveFromStore). nil falls back to the newest sample
	// in Store.
	Live func(ctx context.Context) (servers, tunnels int)
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

// RequireAdmin wraps a handler in the service-admin gate.
func (d *Deps) RequireAdmin(h http.HandlerFunc) http.HandlerFunc {
	if d.Gate == nil {
		return func(w http.ResponseWriter, _ *http.Request) {
			writeErr(w, http.StatusForbidden, "admin_access_denied")
		}
	}
	return d.Gate(h).ServeHTTP
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
	if d.Audit != nil {
		e := audit.Entry{Action: audit.ActionAdminErase, TargetKind: "user", TargetID: id,
			Detail: map[string]any{"push_log_rows": rep.PushRows}, IP: middleware.GetClientIP(r.Context())}
		if admin, ok := authmw.ServiceAdmin(r.Context()); ok {
			e.ActorID, e.ActorEmail = admin.ID, admin.Email
		}
		_ = d.Audit.Write(r.Context(), e)
	}
	writeJSON(w, http.StatusOK, rep)
}

func (d *Deps) live(r *http.Request) (int, int) {
	if d.Live != nil {
		return d.Live(r.Context())
	}
	if d.Store != nil {
		return LiveFromStore(d.Store, time.Now)(r.Context())
	}
	return 0, 0
}

// LiveFromStore reads the gauges the relay role flushed to the shared
// database (the api and relay roles are separate processes, README D8).
// A missing or stale sample reads as nothing connected.
func LiveFromStore(st interface {
	LatestGauges(ctx context.Context, now time.Time, maxAge time.Duration) (int, int, bool, error)
}, now func() time.Time) func(ctx context.Context) (int, int) {
	return func(ctx context.Context) (int, int) {
		servers, tunnels, _, err := st.LatestGauges(ctx, now(), metricspkg.LiveSampleMaxAge)
		if err != nil {
			return 0, 0
		}
		return servers, tunnels
	}
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

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, kind string) {
	writeJSON(w, code, map[string]string{"error": kind})
}
