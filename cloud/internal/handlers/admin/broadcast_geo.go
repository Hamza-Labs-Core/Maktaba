package admin

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
)

var isoCountry = regexp.MustCompile(`^[A-Z]{2}$`)

type geoDenyEntry struct {
	Country string `json:"country"`
	Reason  string `json:"reason"`
	AddedBy string `json:"added_by"`
}

// GeoDeny is Maktaba's broadcast country deny list (story 50.23 §5).
func (d *Deps) GeoDeny(w http.ResponseWriter, r *http.Request) {
	rows, err := d.DB.QueryContext(r.Context(), `SELECT country, reason, added_by FROM broadcast_geo_deny ORDER BY country`)
	if err != nil {
		d.fail(w, r, "load geo deny", err)
		return
	}
	defer func() { _ = rows.Close() }()
	out := []geoDenyEntry{}
	for rows.Next() {
		var e geoDenyEntry
		if err := rows.Scan(&e.Country, &e.Reason, &e.AddedBy); err != nil {
			d.fail(w, r, "scan geo deny", err)
			return
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		d.fail(w, r, "load geo deny", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"countries": out})
}

type geoDenyReq struct {
	Countries []string `json:"countries"`
	Reason    string   `json:"reason"`
}

// SetGeoDeny replaces Maktaba's broadcast country deny list, with a required reason, audited
// in the same transaction. The cloud merges it into every ingest token it signs from now on;
// the edge applies it within the hour, as each home refreshes its tokens.
func (d *Deps) SetGeoDeny(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req geoDenyReq
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeErr(w, http.StatusBadRequest, "reason_required", "give a reason; it goes in the audit log")
		return
	}
	if len(reason) > 1000 {
		reason = reason[:1000]
	}
	seen := map[string]bool{}
	countries := []string{}
	for _, c := range req.Countries {
		c = strings.ToUpper(strings.TrimSpace(c))
		if !isoCountry.MatchString(c) {
			writeErr(w, http.StatusUnprocessableEntity, "bad_country", "countries are ISO 3166 two-letter codes")
			return
		}
		if !seen[c] {
			seen[c] = true
			countries = append(countries, c)
		}
	}
	sort.Strings(countries)
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		d.fail(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	var prev []string
	rows, err := tx.QueryContext(ctx, `SELECT country FROM broadcast_geo_deny ORDER BY country FOR UPDATE`)
	if err != nil {
		d.fail(w, r, "load geo deny", err)
		return
	}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			_ = rows.Close()
			d.fail(w, r, "scan geo deny", err)
			return
		}
		prev = append(prev, c)
	}
	_ = rows.Close()
	e := d.entry(r, audit.ActionAdminBroadcastGeo, "geo_deny", map[string]any{"reason": reason,
		"countries": countries, "previous": prev})
	by := e.ActorEmail
	if _, err := tx.ExecContext(ctx, `DELETE FROM broadcast_geo_deny`); err != nil {
		d.fail(w, r, "clear geo deny", err)
		return
	}
	for _, c := range countries {
		if _, err := tx.ExecContext(ctx, `INSERT INTO broadcast_geo_deny (country, reason, added_by) VALUES ($1, $2, $3)`,
			c, reason, by); err != nil {
			d.fail(w, r, "set geo deny", err)
			return
		}
	}
	if err := audit.WriteTx(ctx, tx, e); err != nil {
		d.fail(w, r, "audit", err)
		return
	}
	if err := tx.Commit(); err != nil {
		d.fail(w, r, "commit", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"countries": countries})
}
