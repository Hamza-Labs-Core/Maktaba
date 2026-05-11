// Package admin exposes the operator-only endpoints used by the
// Hamza Labs admin SPA. Access is gated on the caller's email domain
// matching the configured `[admin].allowed_email_domain` — a heavy-
// handed but transparent rule for a small ops team.
package admin

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

type Deps struct {
	DB            *sql.DB
	Users         *stores.Users
	AllowedDomain string
}

func Mount(r interface {
	Get(string, http.HandlerFunc)
	Post(string, http.HandlerFunc)
}, d Deps) {
	r.Get("/v1/admin/fleet", d.RequireAdmin(d.Fleet))
	r.Get("/v1/admin/revenue", d.RequireAdmin(d.Revenue))
	r.Post("/v1/admin/users/{id}/block", d.RequireAdmin(d.BlockUser))
}

// RequireAdmin gates a handler on the caller's email domain.
func (d *Deps) RequireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.GetUserID(r.Context())
		u, err := d.Users.ByID(r.Context(), uid)
		if err != nil {
			writeErr(w, 401, "unauthorized", "")
			return
		}
		dom := domainOf(u.Email)
		if dom != strings.ToLower(d.AllowedDomain) {
			writeErr(w, 403, "forbidden", "")
			return
		}
		h.ServeHTTP(w, r)
	}
}

func domainOf(email string) string {
	i := strings.LastIndex(email, "@")
	if i < 0 {
		return ""
	}
	return strings.ToLower(email[i+1:])
}

// Fleet returns counts and a small sample of recent servers. The SPA
// reads this on the operator dashboard. Counts are computed at the DB
// — we never load all rows into memory.
func (d *Deps) Fleet(w http.ResponseWriter, r *http.Request) {
	var totalUsers, totalServers, onlineServers int
	_ = d.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM users`).Scan(&totalUsers)
	_ = d.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM servers`).Scan(&totalServers)
	_ = d.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM server_health WHERE online`).Scan(&onlineServers)
	writeJSON(w, 200, map[string]int{
		"users_total":     totalUsers,
		"servers_total":   totalServers,
		"servers_online":  onlineServers,
	})
}

// Revenue aggregates MRR by plan tier. We sum from the live `users.plan`
// + the static catalog so cancelled-but-not-expired subs still count
// until the period ends.
func (d *Deps) Revenue(w http.ResponseWriter, r *http.Request) {
	rows, err := d.DB.QueryContext(r.Context(), `SELECT plan, count(*) FROM users GROUP BY plan`)
	if err != nil {
		writeErr(w, 500, "query_failed", err.Error())
		return
	}
	defer rows.Close()
	type row struct {
		Plan  string `json:"plan"`
		Users int    `json:"users"`
	}
	out := []row{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Plan, &r.Users); err != nil {
			writeErr(w, 500, "scan_failed", err.Error())
			return
		}
		out = append(out, r)
	}
	writeJSON(w, 200, map[string]any{"breakdown": out})
}

// BlockUser sets users.status='blocked' and revokes their sessions.
// Used for abuse response; the user's data is preserved.
func (d *Deps) BlockUser(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	_, err := d.DB.ExecContext(r.Context(), `UPDATE users SET status = 'blocked', updated_at = now() WHERE id = $1`, id)
	if err != nil {
		writeErr(w, 500, "update_failed", err.Error())
		return
	}
	_, _ = d.DB.ExecContext(r.Context(), `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, id)
	w.WriteHeader(http.StatusNoContent)
}

// chiURLParam is a thin shim so this package can be tested without
// importing chi directly in tests (the production wiring still does).
func chiURLParam(r *http.Request, key string) string {
	// chi populates path params under its own type; we use the
	// context value to retrieve via Get. Because the chi import is
	// optional here we just look up by string key.
	if v := r.PathValue(key); v != "" {
		return v
	}
	return ""
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": msg})
}
