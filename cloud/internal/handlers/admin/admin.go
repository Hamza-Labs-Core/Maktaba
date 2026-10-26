// Package admin exposes the service-admin (operator) console API under
// /v1/admin/*: account search and detail, suspension, fleet and server
// health, revenue, abuse handling, the audit log and the anonymous
// usage data (docs/usage-data.md)
// (docs/accounts-billing-admin.md).
//
// Mount puts every route behind Deps.Gate — in production
// auth/middleware.RequireServiceAdmin, which reads the cloud role from
// the database on each request. A cloud account without the role, and
// anything a home server calls an "admin", gets 403. Every mutation and
// every account-detail view writes an audit row; a mutation whose audit
// row cannot be written does not happen (same transaction).
package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/abuse"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	authmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/sessions"
	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	billingh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/license"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

type Deps struct {
	DB       *sql.DB
	Users    *stores.Users
	Sessions *sessions.Store
	Abuse    *abuse.Detector
	Audit    *audit.Log
	Logger   *slog.Logger
	// Gate admits service admins only. A nil Gate refuses everyone.
	Gate func(http.Handler) http.Handler
	// LiveTunnels reports connected tunnels from the relay's flushed
	// gauges; optional.
	LiveTunnels func(ctx context.Context) (servers, tunnels int)
	Now         func() time.Time
	// Licenses is the license record (Epic 16). Nil ⇒ the license
	// routes answer 503.
	Licenses *license.Issuer
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Mount registers the console routes, all behind the gate.
func Mount(r chi.Router, d *Deps) {
	gate := d.Gate
	if gate == nil {
		gate = func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeErr(w, http.StatusForbidden, "admin_access_denied", "")
			})
		}
	}
	r.Group(func(g chi.Router) {
		g.Use(gate)
		g.Get("/v1/admin/accounts", d.ListAccounts)
		g.Get("/v1/admin/accounts/{id}", d.AccountDetail)
		g.Post("/v1/admin/accounts/{id}/suspend", d.Suspend)
		g.Post("/v1/admin/accounts/{id}/unsuspend", d.Unsuspend)
		g.Get("/v1/admin/fleet", d.Fleet)
		g.Get("/v1/admin/servers", d.Servers)
		g.Get("/v1/admin/revenue", d.Revenue)
		g.Get("/v1/admin/abuse", d.AbuseOverview)
		g.Post("/v1/admin/abuse/block", d.Block)
		g.Post("/v1/admin/abuse/unblock", d.Unblock)
		g.Get("/v1/admin/audit", d.AuditLog)
		g.Get("/v1/admin/licenses", d.ListLicenses)
		g.Post("/v1/admin/licenses/{id}/revoke", d.RevokeLicense)
		g.Post("/v1/admin/licenses/{id}/unrevoke", d.UnrevokeLicense)
		g.Get("/v1/admin/usage", d.Usage)
	})
}

func pageParams(r *http.Request, def, most int) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > most {
		limit = def
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// ListAccounts searches accounts by email, name or id.
func (d *Deps) ListAccounts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := pageParams(r, 50, 100)
	list, total, err := d.Users.Search(r.Context(), stores.AccountQuery{
		Q: q.Get("q"), Status: q.Get("status"), Plan: q.Get("plan"), Limit: limit, Offset: offset,
	})
	if err != nil {
		d.fail(w, r, "search accounts", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": list, "total": total})
}

type accountView struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	EmailVerified   bool       `json:"email_verified"`
	DisplayName     string     `json:"display_name,omitempty"`
	Plan            string     `json:"plan"`
	Status          string     `json:"status"`
	Role            string     `json:"role"`
	SuspendedAt     *time.Time `json:"suspended_at,omitempty"`
	SuspendedReason string     `json:"suspended_reason,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty"`
	Identities      []string   `json:"identities"`
}

type subscriptionView struct {
	Plan              string     `json:"plan"`
	Status            string     `json:"status"`
	Interval          string     `json:"interval,omitempty"`
	CurrentPeriodEnd  *time.Time `json:"current_period_end,omitempty"`
	CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
	StripeCustomerID  string     `json:"stripe_customer_id,omitempty"`
}

type serverView struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Slug           string     `json:"slug"`
	OwnerEmail     string     `json:"owner_email,omitempty"`
	Plan           string     `json:"plan"`
	Version        string     `json:"version,omitempty"`
	Online         bool       `json:"online"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
	LastHeartbeat  *time.Time `json:"last_heartbeat,omitempty"`
	RelayLatencyMS *int       `json:"relay_latency_ms,omitempty"`
}

// AccountDetail is the operator's view of one account. Viewing it is
// audited: it shows personal data.
func (d *Deps) AccountDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	u, err := d.lookup(ctx, id)
	if errors.Is(err, stores.ErrUserNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		d.fail(w, r, "load account", err)
		return
	}
	acct := accountView{ID: u.ID, Email: u.Email, EmailVerified: u.EmailVerified, DisplayName: u.DisplayName.String,
		Plan: u.Plan, Status: u.Status, Role: u.Role, SuspendedReason: u.SuspendedReason.String, CreatedAt: u.CreatedAt,
		Identities: []string{}}
	if u.SuspendedAt.Valid {
		t := u.SuspendedAt.Time
		acct.SuspendedAt = &t
	}
	if u.LastLoginAt.Valid {
		t := u.LastLoginAt.Time
		acct.LastLoginAt = &t
	}
	ids, err := d.Users.Identities(ctx, id)
	if err != nil {
		d.fail(w, r, "load identities", err)
		return
	}
	for _, i := range ids {
		acct.Identities = append(acct.Identities, i.Provider)
	}
	sub, err := d.subscription(ctx, id)
	if err != nil {
		d.fail(w, r, "load subscription", err)
		return
	}
	servers, _, err := d.servers(ctx, "s.owner_user_id = $1", []any{id}, 100, 0)
	if err != nil {
		d.fail(w, r, "load servers", err)
		return
	}
	month := billingpkg.MonthStart(d.now())
	_, used, err := billingh.AccountUsage(ctx, d.DB, id, month)
	if err != nil {
		d.fail(w, r, "load usage", err)
		return
	}
	tier, ok := billingpkg.Tiers[u.Plan]
	if !ok {
		tier = billingpkg.Tiers[billingpkg.PlanFree]
	}
	active, err := d.Sessions.CountActive(ctx, id)
	if err != nil {
		d.fail(w, r, "count sessions", err)
		return
	}
	signals, err := d.Abuse.RecentSignals(ctx, &abuse.Subject{Kind: "user", ID: id}, 50)
	if err != nil {
		d.fail(w, r, "load abuse signals", err)
		return
	}
	entries, _, err := d.Audit.List(ctx, audit.Filter{TargetID: id, Limit: 20})
	if err != nil {
		d.fail(w, r, "load audit", err)
		return
	}
	if err := d.Audit.Write(ctx, d.entry(r, audit.ActionAdminViewUser, id, nil)); err != nil {
		d.fail(w, r, "audit view", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"account":         acct,
		"subscription":    sub,
		"servers":         servers,
		"usage":           map[string]any{"month": month.Format("2006-01"), "used_bytes": used, "limit_bytes": tier.BandwidthBytesPerMo},
		"sessions_active": active,
		"abuse":           signals,
		"audit":           entries,
	})
}

// lookup loads an account by id; a malformed id is not found.
func (d *Deps) lookup(ctx context.Context, id string) (stores.User, error) {
	if !looksLikeUUID(id) {
		return stores.User{}, stores.ErrUserNotFound
	}
	return d.Users.ByID(ctx, id)
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'):
		default:
			return false
		}
	}
	return true
}

func (d *Deps) subscription(ctx context.Context, uid string) (*subscriptionView, error) {
	var s subscriptionView
	var interval, cust sql.NullString
	var end sql.NullTime
	err := d.DB.QueryRowContext(ctx, `
        SELECT plan, status, interval, current_period_end, cancel_at_period_end, stripe_customer_id
        FROM subscriptions WHERE user_id = $1`, uid).Scan(&s.Plan, &s.Status, &interval, &end, &s.CancelAtPeriodEnd, &cust)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.Interval, s.StripeCustomerID = interval.String, cust.String
	if end.Valid {
		t := end.Time
		s.CurrentPeriodEnd = &t
	}
	return &s, nil
}

type reasonReq struct {
	Reason string `json:"reason"`
}

func readReason(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req reasonReq
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
		return "", false
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeErr(w, http.StatusBadRequest, "reason_required", "give a reason; it goes in the audit log")
		return "", false
	}
	if len(reason) > 1000 {
		reason = reason[:1000]
	}
	return reason, true
}

// Suspend blocks an account from signing in and signs every device out.
// Billing is left alone (Stripe cancels separately). An operator cannot
// suspend themselves or another service admin — demote first, from the
// CLI.
func (d *Deps) Suspend(w http.ResponseWriter, r *http.Request) {
	d.setStatus(w, r, stores.StatusSuspended)
}

// Unsuspend reinstates an account.
func (d *Deps) Unsuspend(w http.ResponseWriter, r *http.Request) {
	d.setStatus(w, r, stores.StatusActive)
}

func (d *Deps) setStatus(w http.ResponseWriter, r *http.Request, status string) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	reason, ok := readReason(w, r)
	if !ok {
		return
	}
	target, err := d.lookup(ctx, id)
	if errors.Is(err, stores.ErrUserNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		d.fail(w, r, "load account", err)
		return
	}
	action := audit.ActionAdminUnsuspend
	if status == stores.StatusSuspended {
		action = audit.ActionAdminSuspend
		if target.Role == stores.RoleServiceAdmin {
			writeErr(w, http.StatusConflict, "cannot_suspend_admin", "a service admin cannot be suspended; revoke the role first")
			return
		}
	}
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		d.fail(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	if err := d.Users.SetStatus(ctx, tx, id, status, reason); err != nil {
		d.fail(w, r, "set status", err)
		return
	}
	if status == stores.StatusSuspended {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = COALESCE(revoked_at, now()), revoke_reason = $2 WHERE user_id = $1 AND (revoked_at IS NULL OR revoke_reason = $3)`, id, sessions.ReasonRevoked, sessions.ReasonRotated); err != nil {
			d.fail(w, r, "revoke sessions", err)
			return
		}
	}
	if err := audit.WriteTx(ctx, tx, d.entry(r, action, id, map[string]any{"reason": reason, "previous_status": target.Status})); err != nil {
		d.fail(w, r, "audit", err)
		return
	}
	if err := tx.Commit(); err != nil {
		d.fail(w, r, "commit", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Fleet is the operator dashboard's headline numbers. A server counts
// as stale when it is marked online but has not sent a heartbeat for 5
// minutes.
func (d *Deps) Fleet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var users, suspended, servers, online, stale int
	err := d.DB.QueryRowContext(ctx, `
        SELECT (SELECT count(*) FROM users),
               (SELECT count(*) FROM users WHERE status = 'suspended'),
               (SELECT count(*) FROM servers),
               (SELECT count(*) FROM server_health WHERE online),
               (SELECT count(*) FROM server_health WHERE online AND (last_heartbeat IS NULL OR last_heartbeat < now() - INTERVAL '5 minutes'))
    `).Scan(&users, &suspended, &servers, &online, &stale)
	if err != nil {
		d.fail(w, r, "fleet counts", err)
		return
	}
	out := map[string]any{
		"users_total":     users,
		"users_suspended": suspended,
		"servers_total":   servers,
		"servers_online":  online,
		"servers_stale":   stale,
	}
	if d.LiveTunnels != nil {
		_, tunnels := d.LiveTunnels(ctx)
		out["tunnels_live"] = tunnels
	}
	writeJSON(w, http.StatusOK, out)
}

// Servers lists linked servers with their health.
func (d *Deps) Servers(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r, 50, 200)
	cond, args := "true", []any{}
	switch r.URL.Query().Get("status") {
	case "online":
		cond = "COALESCE(h.online, false)"
	case "offline":
		cond = "NOT COALESCE(h.online, false)"
	}
	list, total, err := d.servers(r.Context(), cond, args, limit, offset)
	if err != nil {
		d.fail(w, r, "list servers", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": list, "total": total})
}

func (d *Deps) servers(ctx context.Context, cond string, args []any, limit, offset int) ([]serverView, int, error) {
	var total int
	if err := d.DB.QueryRowContext(ctx, `SELECT count(*) FROM servers s LEFT JOIN server_health h ON h.server_id = s.id WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	n := len(args)
	args = append(args, limit, offset)
	rows, err := d.DB.QueryContext(ctx, `
        SELECT s.id, s.name, s.slug, u.email, s.plan, COALESCE(s.version,''), COALESCE(h.online,false),
               s.last_seen_at, h.last_heartbeat, h.relay_latency_ms
        FROM servers s
        JOIN users u ON u.id = s.owner_user_id
        LEFT JOIN server_health h ON h.server_id = s.id
        WHERE `+cond+`
        ORDER BY s.last_seen_at DESC NULLS LAST, s.created_at DESC
        LIMIT $`+strconv.Itoa(n+1)+` OFFSET $`+strconv.Itoa(n+2), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []serverView{}
	for rows.Next() {
		var s serverView
		var seen, hb sql.NullTime
		var lat sql.NullInt32
		if err := rows.Scan(&s.ID, &s.Name, &s.Slug, &s.OwnerEmail, &s.Plan, &s.Version, &s.Online, &seen, &hb, &lat); err != nil {
			return nil, 0, err
		}
		if seen.Valid {
			t := seen.Time
			s.LastSeenAt = &t
		}
		if hb.Valid {
			t := hb.Time
			s.LastHeartbeat = &t
		}
		if lat.Valid {
			v := int(lat.Int32)
			s.RelayLatencyMS = &v
		}
		out = append(out, s)
	}
	return out, total, rows.Err()
}

// Revenue is MRR from the subscriptions table priced with the plan
// catalog. A subscription counts while it is active, trialing or
// past_due (Stripe is still collecting); yearly plans count at their
// monthly catalog price.
func (d *Deps) Revenue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	type planRow struct {
		Plan     string `json:"plan"`
		Users    int    `json:"users"`
		Active   int    `json:"active"`
		MRRCents int64  `json:"mrr_cents"`
	}
	rows, err := d.DB.QueryContext(ctx, `
        SELECT u.plan, count(*),
               count(*) FILTER (WHERE s.status IN ('active','trialing','past_due'))
        FROM users u LEFT JOIN subscriptions s ON s.user_id = u.id
        GROUP BY u.plan ORDER BY u.plan`)
	if err != nil {
		d.fail(w, r, "revenue", err)
		return
	}
	defer rows.Close()
	out := []planRow{}
	var mrr int64
	var paid int
	for rows.Next() {
		var p planRow
		if err := rows.Scan(&p.Plan, &p.Users, &p.Active); err != nil {
			d.fail(w, r, "revenue scan", err)
			return
		}
		if t, ok := billingpkg.Tiers[p.Plan]; ok && t.PricePerMonthCents > 0 {
			p.MRRCents = int64(p.Active) * int64(t.PricePerMonthCents)
			paid += p.Active
		}
		mrr += p.MRRCents
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		d.fail(w, r, "revenue rows", err)
		return
	}
	var pastDue, cancelling int
	if err := d.DB.QueryRowContext(ctx, `
        SELECT count(*) FILTER (WHERE status = 'past_due'),
               count(*) FILTER (WHERE cancel_at_period_end AND status IN ('active','trialing'))
        FROM subscriptions`).Scan(&pastDue, &cancelling); err != nil {
		d.fail(w, r, "revenue status", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"currency": "usd", "mrr_cents": mrr, "arr_cents": mrr * 12,
		"active_paid": paid, "past_due": pastDue, "cancelling": cancelling, "breakdown": out,
	})
}

// AbuseOverview lists recent signals and the blocklist.
func (d *Deps) AbuseOverview(w http.ResponseWriter, r *http.Request) {
	limit, _ := pageParams(r, 100, 200)
	signals, err := d.Abuse.RecentSignals(r.Context(), nil, limit)
	if err != nil {
		d.fail(w, r, "abuse signals", err)
		return
	}
	block, err := d.Abuse.Blocklist(r.Context())
	if err != nil {
		d.fail(w, r, "blocklist", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signals": signals, "blocklist": block})
}

type blockReq struct {
	SubjectKind    string `json:"subject_kind"`
	Subject        string `json:"subject"`
	Reason         string `json:"reason"`
	ExpiresInHours int    `json:"expires_in_hours"`
}

func readBlock(w http.ResponseWriter, r *http.Request) (blockReq, bool) {
	var req blockReq
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
		return req, false
	}
	req.Subject, req.Reason = strings.TrimSpace(req.Subject), strings.TrimSpace(req.Reason)
	switch req.SubjectKind {
	case "user", "server", "ip":
	default:
		writeErr(w, http.StatusBadRequest, "bad_subject_kind", "subject_kind is user, server or ip")
		return req, false
	}
	if req.Subject == "" {
		writeErr(w, http.StatusBadRequest, "subject_required", "")
		return req, false
	}
	if req.Reason == "" {
		writeErr(w, http.StatusBadRequest, "reason_required", "give a reason; it goes in the audit log")
		return req, false
	}
	return req, true
}

// Block adds a user, server or IP to the blocklist.
func (d *Deps) Block(w http.ResponseWriter, r *http.Request) {
	req, ok := readBlock(w, r)
	if !ok {
		return
	}
	if req.SubjectKind == "user" {
		if u, err := d.lookup(r.Context(), req.Subject); err == nil && u.Role == stores.RoleServiceAdmin {
			writeErr(w, http.StatusConflict, "cannot_suspend_admin", "a service admin cannot be blocked; revoke the role first")
			return
		}
	}
	var exp *time.Time
	if req.ExpiresInHours > 0 {
		t := d.now().Add(time.Duration(req.ExpiresInHours) * time.Hour)
		exp = &t
	}
	if err := d.Audit.Write(r.Context(), d.entry(r, audit.ActionAdminBlock, req.Subject,
		map[string]any{"subject_kind": req.SubjectKind, "reason": req.Reason, "expires_in_hours": req.ExpiresInHours})); err != nil {
		d.fail(w, r, "audit", err)
		return
	}
	if err := d.Abuse.Block(r.Context(), abuse.Subject{Kind: req.SubjectKind, ID: req.Subject}, req.Reason, exp); err != nil {
		d.fail(w, r, "block", err)
		return
	}
	if req.SubjectKind == "user" && looksLikeUUID(req.Subject) {
		if err := d.Sessions.RevokeAllForUser(r.Context(), req.Subject); err != nil {
			d.Logger.Error("admin: revoke sessions of blocked user", "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// Unblock removes a subject from the blocklist.
func (d *Deps) Unblock(w http.ResponseWriter, r *http.Request) {
	req, ok := readBlock(w, r)
	if !ok {
		return
	}
	if err := d.Audit.Write(r.Context(), d.entry(r, audit.ActionAdminUnblock, req.Subject,
		map[string]any{"subject_kind": req.SubjectKind, "reason": req.Reason})); err != nil {
		d.fail(w, r, "audit", err)
		return
	}
	found, err := d.Abuse.Unblock(r.Context(), abuse.Subject{Kind: req.SubjectKind, ID: req.Subject})
	if err != nil {
		d.fail(w, r, "unblock", err)
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AuditLog pages through the audit log, newest first.
func (d *Deps) AuditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := pageParams(r, audit.MaxPage, audit.MaxPage)
	entries, total, err := d.Audit.List(r.Context(), audit.Filter{
		ActorID: q.Get("actor"), Action: q.Get("action"), TargetID: q.Get("target"), Limit: limit, Offset: offset,
	})
	if err != nil {
		d.fail(w, r, "audit list", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "total": total})
}

// entry builds an audit row for an action by the admitted operator.
func (d *Deps) entry(r *http.Request, action, target string, detail map[string]any) audit.Entry {
	e := audit.Entry{Action: action, TargetKind: "user", TargetID: target, Detail: detail, IP: middleware.GetClientIP(r.Context())}
	if strings.HasPrefix(action, "admin.license.") {
		e.TargetKind = "license"
	}
	if strings.HasPrefix(action, "admin.abuse.") {
		if k, _ := detail["subject_kind"].(string); k != "" {
			e.TargetKind = k
		}
	}
	if u, ok := authmw.ServiceAdmin(r.Context()); ok {
		e.ActorID, e.ActorEmail = u.ID, u.Email
	}
	return e
}

func (d *Deps) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	d.Logger.Error("admin: "+what, "err", err, "request_id", middleware.GetRequestID(r.Context()))
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

// ListLicenses lists the issued licenses with their revocations.
func (d *Deps) ListLicenses(w http.ResponseWriter, r *http.Request) {
	if d.Licenses == nil {
		writeErr(w, http.StatusServiceUnavailable, "licensing_disabled", "")
		return
	}
	limit, offset := pageParams(r, 50, 200)
	list, total, err := d.Licenses.List(r.Context(), limit, offset)
	if err != nil {
		d.fail(w, r, "list licenses", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"licenses": list, "total": total})
}

// RevokeLicense puts a license id on the signed revocation list: the
// home holding it locks at its next check (a plan license within a
// minute, an offline one within a day).
func (d *Deps) RevokeLicense(w http.ResponseWriter, r *http.Request) {
	d.setRevoked(w, r, true)
}

// UnrevokeLicense takes a license id off the list.
func (d *Deps) UnrevokeLicense(w http.ResponseWriter, r *http.Request) {
	d.setRevoked(w, r, false)
}

func (d *Deps) setRevoked(w http.ResponseWriter, r *http.Request, revoke bool) {
	if d.Licenses == nil {
		writeErr(w, http.StatusServiceUnavailable, "licensing_disabled", "")
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	reason, ok := readReason(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	action := "admin.license.unrevoke"
	if revoke {
		action = "admin.license.revoke"
		if err := d.Licenses.Revoke(ctx, id, reason, middleware.GetUserID(ctx)); err != nil {
			d.fail(w, r, "revoke license", err)
			return
		}
	} else if found, err := d.Licenses.Unrevoke(ctx, id); err != nil {
		d.fail(w, r, "unrevoke license", err)
		return
	} else if !found {
		writeErr(w, http.StatusNotFound, "not_found", "that license is not revoked")
		return
	}
	if d.Audit != nil {
		if err := d.Audit.Write(ctx, d.entry(r, action, id, map[string]any{"reason": reason})); err != nil {
			d.Logger.Error("admin: audit license change", "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
