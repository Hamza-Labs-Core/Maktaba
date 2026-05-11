// Package health serves the server-status dashboard endpoint
// (Story 25.16). The web SPA polls this for live state for every
// server the user owns: online/offline, relay/direct latency, CPU,
// memory, storage. The query joins servers + server_health and keeps
// the response small enough to poll every 5 s.
package health

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
)

type Deps struct{ DB *sql.DB }

type serverStatus struct {
	ID              string    `json:"id"`
	Slug            string    `json:"slug"`
	Name            string    `json:"name"`
	Online          bool      `json:"online"`
	LastSeen        *time.Time `json:"last_seen_at,omitempty"`
	RelayLatencyMS  int       `json:"relay_latency_ms"`
	DirectLatencyMS int       `json:"direct_latency_ms"`
	CPUPct          float32   `json:"cpu_pct"`
	MemPct          float32   `json:"mem_pct"`
	StoragePct      float32   `json:"storage_pct"`
}

func (d *Deps) Mount(r interface {
	Get(pattern string, h http.HandlerFunc)
}) {
	r.Get("/v1/dashboard/servers", d.List)
}

func (d *Deps) List(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	rows, err := d.DB.QueryContext(r.Context(), `
        SELECT s.id, s.slug, s.name, s.last_seen_at,
               COALESCE(h.online, false),
               COALESCE(h.relay_latency_ms, 0),
               COALESCE(h.direct_latency_ms, 0),
               COALESCE(h.cpu_pct, 0),
               COALESCE(h.mem_pct, 0),
               COALESCE(h.storage_pct, 0)
        FROM servers s
        LEFT JOIN server_health h ON h.server_id = s.id
        WHERE s.owner_user_id = $1
        ORDER BY s.created_at DESC
    `, uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []serverStatus{}
	for rows.Next() {
		var s serverStatus
		var ls sql.NullTime
		if err := rows.Scan(&s.ID, &s.Slug, &s.Name, &ls, &s.Online, &s.RelayLatencyMS, &s.DirectLatencyMS, &s.CPUPct, &s.MemPct, &s.StoragePct); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if ls.Valid {
			t := ls.Time
			s.LastSeen = &t
		}
		out = append(out, s)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"servers": out})
}
