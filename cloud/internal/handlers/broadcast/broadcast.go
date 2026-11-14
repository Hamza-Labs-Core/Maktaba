// Package broadcast (handlers) is home broadcasting's cloud API (Epic 50 story 50.20):
//
//	GET  /v1/broadcast/agreement         the account: the current Broadcaster agreement and
//	                                     whether it accepted it
//	POST /v1/broadcast/agreement         {version}: accept it (409 agreement_version_stale)
//	POST /v1/broadcast/channels          a home (basic auth server_id:secret): publish a channel;
//	                                     402 broadcast_needs_plan, 403 broadcast_not_enabled,
//	                                     402 broadcast_limit {limit}, 409 broadcast_agreement_required
//	DELETE /v1/broadcast/channels/{id}   a home: unpublish one
//	GET  /v1/broadcast/state             a home: its PublisherState (the Broadcast page's usage)
//
// Story 50.26 adds viewer access (invites.go). Story 50.22 adds the ladder, the ingest token (50.21) and PATCH to the channel routes.
package broadcast

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Deps are the routes' dependencies.
type Deps struct {
	DB      *sql.DB
	Servers *stores.Servers
	Store   *billingpkg.BroadcastStore
	Logger  *slog.Logger
	// IPSalt keys the hash of the address an agreement was accepted from (never the address).
	IPSalt []byte

	// Viewer access (story 50.26). Key signs invites and the access posts (nil: invites answer
	// 503 broadcast_disabled); OpenAccess is broadcast.open_access.enabled; WatchURL is the
	// invite links' origin (default https://watch.maktaba.tv); Edge receives the access posts
	// (nil: nothing is pushed).
	Key        *bc.Key
	OpenAccess bool
	WatchURL   string
	Edge       EdgeClient
	Now        func() time.Time
}

// MountAgent registers the routes a home calls with its server credential.
func MountAgent(r chi.Router, d *Deps) {
	r.Post("/v1/broadcast/channels", d.Publish)
	r.Delete("/v1/broadcast/channels/{id}", d.Unpublish)
	r.Get("/v1/broadcast/state", d.State)
}

// MountUser registers the account routes (inside the bearer-token group).
func MountUser(r chi.Router, d *Deps) {
	r.Get("/v1/broadcast/agreement", d.GetAgreement)
	r.Post("/v1/broadcast/agreement", d.AcceptAgreement)
}

type agreementView struct {
	Version  int  `json:"version"`
	Accepted bool `json:"accepted"`
	// AcceptedVersion is the newest version the account accepted (0: none).
	AcceptedVersion int `json:"accepted_version"`
}

// GetAgreement is the current agreement's version and the caller's acceptance.
func (d *Deps) GetAgreement(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	var newest int
	if err := d.DB.QueryRowContext(r.Context(), `SELECT COALESCE(max(version), 0) FROM broadcast_agreements WHERE account_id = $1`, uid).Scan(&newest); err != nil {
		d.fail(w, r, "load agreement", err)
		return
	}
	v := billingpkg.BroadcastAgreementVersion
	writeJSON(w, http.StatusOK, agreementView{Version: v, Accepted: newest >= v, AcceptedVersion: newest})
}

// AcceptAgreement records the caller's acceptance of the current version, with the salted hash
// of the address it came from.
func (d *Deps) AcceptAgreement(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version int `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "body must be {version}")
		return
	}
	if body.Version != billingpkg.BroadcastAgreementVersion {
		writeErr(w, http.StatusConflict, "agreement_version_stale", "a newer Broadcaster agreement is out; read it and accept it")
		return
	}
	uid := middleware.GetUserID(r.Context())
	if _, err := d.DB.ExecContext(r.Context(), `
        INSERT INTO broadcast_agreements (account_id, version, ip_hash) VALUES ($1, $2, $3)
        ON CONFLICT (account_id, version) DO NOTHING`, uid, body.Version, d.ipHash(middleware.GetClientIP(r.Context()))); err != nil {
		d.fail(w, r, "accept agreement", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d *Deps) ipHash(ip string) string {
	if ip == "" {
		return ""
	}
	m := hmac.New(sha256.New, d.IPSalt)
	_, _ = m.Write([]byte(ip))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// agent authenticates a home by its id and secret (basic auth); false after writing 401.
func (d *Deps) agent(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, secret, ok := r.BasicAuth()
	if !ok || d.Servers.VerifySecret(r.Context(), id, secret) != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="maktaba-server"`)
		writeErr(w, http.StatusUnauthorized, "unauthorized", "")
		return "", false
	}
	return id, true
}

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// PublishRequest is a channel the home publishes.
type PublishRequest struct {
	Slug     string `json:"slug"`
	Access   string `json:"access"`
	OnDemand bool   `json:"on_demand"`
	Rungs    int    `json:"rungs"`
	TopKbps  int    `json:"top_kbps"`
}

// Publish runs story 50.20's checks for a new channel in order, naming the first that fails:
// the plan (402 broadcast_needs_plan), the broadcasting gate (403 broadcast_not_enabled), the
// tier's limits (402 broadcast_limit naming the limit), the account's acceptance of the current
// Broadcaster agreement (409 broadcast_agreement_required). Then 201 with the channel.
func (d *Deps) Publish(w http.ResponseWriter, r *http.Request) {
	id, ok := d.agent(w, r)
	if !ok {
		return
	}
	var req PublishRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil || !slugRE.MatchString(req.Slug) {
		writeErr(w, http.StatusBadRequest, "bad_request", "body must be {slug, access, on_demand, rungs, top_kbps}")
		return
	}
	if req.Access == "" {
		req.Access = billingpkg.AccessInvite
	}
	if req.Rungs <= 0 {
		req.Rungs = 1
	}
	ctx := r.Context()
	p, err := d.Store.Publisher(ctx, id)
	if err != nil {
		d.fail(w, r, "load publisher", err)
		return
	}
	tier := p.Tier()
	if tier == nil {
		writeErr(w, http.StatusPaymentRequired, "broadcast_needs_plan", "sharing channels with people outside your home needs the Pro plan")
		return
	}
	if !d.Store.Gated(p) {
		writeErr(w, http.StatusForbidden, "broadcast_not_enabled", "broadcasting isn't open yet")
		return
	}
	limits := p.Limits()
	// the count and the insert under the home's row lock: two publishes at once can't both
	// take the last slot
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		d.fail(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	var live int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM servers WHERE id = $1 FOR UPDATE`, id).Scan(&live); err != nil {
		d.fail(w, r, "lock home", err)
		return
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM broadcast_channels WHERE server_id = $1 AND unpublished_at IS NULL`, id).Scan(&live); err != nil {
		d.fail(w, r, "count channels", err)
		return
	}
	switch {
	case live >= limits.MaxChannels:
		limitErr(w, "max_channels", limits.MaxChannels)
		return
	case !tier.AllowsAccess(req.Access):
		limitErr(w, "access", 0)
		return
	case tier.OnDemandOnly && !req.OnDemand:
		limitErr(w, "on_demand", 0)
		return
	case req.Rungs > tier.MaxRungs:
		limitErr(w, "max_rungs", tier.MaxRungs)
		return
	case req.TopKbps > tier.MaxTopKbps:
		limitErr(w, "max_top_kbps", tier.MaxTopKbps)
		return
	}
	// open (story 50.26): broadcast.open_access.enabled and a verified publisher; a first
	// publish is never verified (7 days since the first publish)
	if req.Access == billingpkg.AccessOpen {
		code, status, err := d.openRefusal(ctx, p)
		if err != nil {
			d.fail(w, r, "open check", err)
			return
		}
		if code != "" {
			writeOpenRefusal(w, code, status)
			return
		}
	}
	accepted, err := d.Store.AgreementAccepted(ctx, p.OwnerID)
	if err != nil {
		d.fail(w, r, "load agreement", err)
		return
	}
	if !accepted {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "broadcast_agreement_required",
			"message": "the account owner must accept the Broadcaster agreement first", "version": billingpkg.BroadcastAgreementVersion})
		return
	}
	var chID string
	err = tx.QueryRowContext(ctx, `
        INSERT INTO broadcast_channels (server_id, slug, access, on_demand, rungs, top_kbps, agreement_version)
        VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id::text`,
		id, req.Slug, req.Access, req.OnDemand, req.Rungs, req.TopKbps, billingpkg.BroadcastAgreementVersion).Scan(&chID)
	if err != nil {
		if isUnique(err) {
			writeErr(w, http.StatusConflict, "slug_taken", "a published channel already has this slug")
			return
		}
		d.fail(w, r, "publish", err)
		return
	}
	if err := BumpAccess(ctx, tx, id); err != nil {
		d.fail(w, r, "bump", err)
		return
	}
	if err := tx.Commit(); err != nil {
		d.fail(w, r, "commit", err)
		return
	}
	d.pushOne(ctx, id)
	writeJSON(w, http.StatusCreated, map[string]any{"id": chID, "slug": req.Slug, "access": req.Access,
		"on_demand": req.OnDemand, "rungs": req.Rungs, "top_kbps": req.TopKbps})
}

func limitErr(w http.ResponseWriter, limit string, value int) {
	writeJSON(w, http.StatusPaymentRequired, map[string]any{"error": "broadcast_limit", "limit": limit, "value": value,
		"message": "your plan doesn't allow this channel; add a channel or unpublish one first"})
}

func isUnique(err error) bool {
	var c interface{ SQLState() string }
	if errors.As(err, &c) {
		return c.SQLState() == "23505"
	}
	return false
}

// Unpublish takes one of the home's channels off (its slot is free at once).
func (d *Deps) Unpublish(w http.ResponseWriter, r *http.Request) {
	id, ok := d.agent(w, r)
	if !ok {
		return
	}
	ch := chi.URLParam(r, "id")
	if !billingpkg.IsUUID(ch) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	d.change(w, r, id, func(tx *sql.Tx) (int, error) {
		res, err := tx.ExecContext(r.Context(), `UPDATE broadcast_channels SET unpublished_at = now() WHERE id = $1 AND server_id = $2 AND unpublished_at IS NULL`, ch, id)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return http.StatusNotFound, nil
		}
		return http.StatusNoContent, nil
	})
}

// State is the home's publisher state: its plan's limits, this month's viewer-hours, its
// pay-as-you-go and the mode for new viewers.
func (d *Deps) State(w http.ResponseWriter, r *http.Request) {
	id, ok := d.agent(w, r)
	if !ok {
		return
	}
	st, err := d.Store.State(r.Context(), id)
	if err != nil {
		d.fail(w, r, "state", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (d *Deps) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	if d.Logger != nil {
		d.Logger.Error("broadcast: "+what, "err", err, "request_id", middleware.GetRequestID(r.Context()))
	}
	writeErr(w, http.StatusInternalServerError, "internal_error", "")
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": msg})
}
