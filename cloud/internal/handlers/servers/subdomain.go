package servers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
)

// SubdomainDeps wraps the dep this small handler needs separately from
// the main servers handler; we keep it in its own struct so the test
// for subdomain reservation can exercise it without the rest.
type SubdomainDeps struct {
	DB *sql.DB
}

// MountSubdomains registers `/v1/servers/{id}/subdomain`. The actual
// DNS provisioning (Cloudflare API call) happens out-of-band in a
// worker; this endpoint just persists the user's chosen slug and
// validates it against `reserved_slugs`.
func MountSubdomains(r interface {
	Post(string, http.HandlerFunc)
	Get(string, http.HandlerFunc)
}, d SubdomainDeps) {
	r.Post("/v1/subdomains/check", d.Check)
	r.Post("/v1/subdomains/claim", d.Claim)
}

type checkReq struct {
	Slug string `json:"slug"`
}

type checkResp struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Check tells the wizard whether a desired slug is free. We deliberately
// allow the user to keep trying — slug availability is not a secret.
func (d *SubdomainDeps) Check(w http.ResponseWriter, r *http.Request) {
	var req checkReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid_body", err.Error())
		return
	}
	slug := strings.ToLower(strings.TrimSpace(req.Slug))
	if !slugRE.MatchString(slug) {
		writeJSON(w, 200, checkResp{Available: false, Reason: "format"})
		return
	}
	var reason string
	err := d.DB.QueryRowContext(r.Context(), `SELECT reason FROM reserved_slugs WHERE slug = $1`, slug).Scan(&reason)
	if err == nil {
		writeJSON(w, 200, checkResp{Available: false, Reason: "reserved:" + reason})
		return
	}
	var n int
	_ = d.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM subdomains WHERE slug = $1`, slug).Scan(&n)
	if n > 0 {
		writeJSON(w, 200, checkResp{Available: false, Reason: "taken"})
		return
	}
	writeJSON(w, 200, checkResp{Available: true})
}

type claimReq struct {
	Slug     string `json:"slug"`
	ServerID string `json:"server_id"`
}

// Claim records the slug for a server. Pre-condition: the caller owns
// the server. We re-check ownership via a JOIN so a forged server_id
// can't be claimed.
func (d *SubdomainDeps) Claim(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	if uid == "" {
		writeErr(w, 401, "unauthorized", "")
		return
	}
	var req claimReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid_body", err.Error())
		return
	}
	slug := strings.ToLower(strings.TrimSpace(req.Slug))
	if !slugRE.MatchString(slug) {
		writeErr(w, 422, "bad_slug", "")
		return
	}
	tx, err := d.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeErr(w, 500, "txn_failed", err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()

	var owner string
	if err := tx.QueryRowContext(r.Context(), `SELECT owner_user_id FROM servers WHERE id = $1`, req.ServerID).Scan(&owner); err != nil {
		writeErr(w, 404, "server_not_found", "")
		return
	}
	if owner != uid {
		writeErr(w, 403, "forbidden", "")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
        INSERT INTO subdomains (slug, server_id) VALUES ($1, $2)
        ON CONFLICT (slug) DO NOTHING
    `, slug, req.ServerID); err != nil {
		writeErr(w, 500, "insert_failed", err.Error())
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE servers SET slug = $1 WHERE id = $2`, slug, req.ServerID); err != nil {
		writeErr(w, 500, "update_failed", err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "commit_failed", err.Error())
		return
	}
	writeJSON(w, 201, map[string]string{"slug": slug})
}
