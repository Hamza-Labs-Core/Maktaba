// Package billing (handlers) wires the billing HTTP surface
// (docs/accounts-billing-admin.md):
//
//	GET  /v1/billing/plans        — plan comparison (public)
//	GET  /v1/billing/me           — current subscription state
//	POST /v1/billing/checkout     — start a Stripe checkout
//	POST /v1/billing/portal       — open the Stripe customer portal
//	GET  /v1/billing/usage        — this month's relay bandwidth
//	POST /v1/billing/webhook      — Stripe webhooks (webhook.go)
package billing

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/abuse"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/sessions"
	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/entitlement"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/license"
	mailpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

type Deps struct {
	DB                 *sql.DB
	Stripe             *billingpkg.StripeClient
	Users              *stores.Users
	Sessions           *sessions.Store
	Abuse              *abuse.Detector
	Audit              *audit.Log
	Mailer             mailpkg.Mailer
	Logger             *slog.Logger
	Entitlements       *entitlement.Signer // nil: entitlement endpoints answer 503
	WebhookSecret      string
	WebURL             string
	PriceIDProMonth    string
	PriceIDFamilyMonth string
	Now                func() time.Time
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// MountPublic registers the routes that need no bearer token: the plan
// catalog, the entitlement verification key, and the Stripe webhook
// (Stripe does not carry our token; it is authenticated by signature).
func MountPublic(r chi.Router, d *Deps) {
	r.Get("/v1/billing/plans", d.Plans)
	r.Get("/.well-known/maktaba-entitlement-key", d.EntitlementKey)
	r.Post("/v1/billing/webhook", d.Webhook)
}

// Mount registers the account routes inside the bearer-token group.
func Mount(r chi.Router, d *Deps) {
	r.Get("/v1/billing/me", d.Me)
	r.Post("/v1/billing/checkout", d.Checkout)
	r.Post("/v1/billing/portal", d.Portal)
	r.Get("/v1/billing/usage", d.Usage)
}

// Plans returns the static catalog from billing.Tiers.
func (d *Deps) Plans(w http.ResponseWriter, _ *http.Request) {
	type viewTier struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		PriceCents  int    `json:"price_cents"`
		Bandwidth   int64  `json:"bandwidth_bytes_per_month"`
		MaxServers  int    `json:"max_servers"`
		MaxStreams  int    `json:"max_concurrent_streams"`
		RelayQoS    string `json:"relay_qos"`
		Transcoding bool   `json:"includes_transcoding"`
		FamilySeats int    `json:"family_seats"`
		Purchasable bool   `json:"purchasable"`
		// HomeTier is the plan the license gives the owner's homes
		// (Epic 16: home or pro; "" for free).
		HomeTier string `json:"home_tier"`
	}
	out := []viewTier{}
	for _, id := range []string{billingpkg.PlanFree, billingpkg.PlanPro, billingpkg.PlanFamily} {
		t := billingpkg.Tiers[id]
		out = append(out, viewTier{
			ID: t.ID, Name: t.Name, PriceCents: t.PricePerMonthCents,
			Bandwidth: t.BandwidthBytesPerMo, MaxServers: t.MaxServers,
			MaxStreams: t.MaxConcurrentStreams, RelayQoS: t.RelayQoS,
			Transcoding: t.IncludesTranscoding, FamilySeats: t.FamilySeats,
			Purchasable: d.Stripe.Configured() && d.priceFor(id) != "",
			HomeTier:    license.TierForPlan(id),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": out})
}

type subRow struct {
	Plan              string
	Interval          sql.NullString
	Status            string
	CustomerID        sql.NullString
	SubscriptionID    sql.NullString
	CurrentPeriodEnd  sql.NullTime
	CancelAtPeriodEnd bool
}

func (d *Deps) subscription(r *http.Request, uid string) (subRow, bool, error) {
	var s subRow
	err := d.DB.QueryRowContext(r.Context(), `
        SELECT plan, interval, status, stripe_customer_id, stripe_subscription_id, current_period_end, cancel_at_period_end
        FROM subscriptions WHERE user_id = $1
    `, uid).Scan(&s.Plan, &s.Interval, &s.Status, &s.CustomerID, &s.SubscriptionID, &s.CurrentPeriodEnd, &s.CancelAtPeriodEnd)
	if errors.Is(err, sql.ErrNoRows) {
		return subRow{Plan: billingpkg.PlanFree, Status: "none"}, false, nil
	}
	return s, err == nil, err
}

type meView struct {
	Plan              string     `json:"plan"`
	Status            string     `json:"status"`
	Interval          string     `json:"interval,omitempty"`
	CurrentPeriodEnd  *time.Time `json:"current_period_end,omitempty"`
	CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
	HasCustomer       bool       `json:"has_customer"`
}

// Me reports the caller's subscription. Plan is the effective plan
// (users.plan), which falls back to free when the subscription lapses.
func (d *Deps) Me(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	u, err := d.Users.ByID(r.Context(), uid)
	if err != nil {
		d.fail(w, r, "load account", err)
		return
	}
	s, _, err := d.subscription(r, uid)
	if err != nil {
		d.fail(w, r, "load subscription", err)
		return
	}
	v := meView{Plan: u.Plan, Status: s.Status, Interval: s.Interval.String,
		CancelAtPeriodEnd: s.CancelAtPeriodEnd, HasCustomer: s.CustomerID.Valid}
	if s.CurrentPeriodEnd.Valid {
		t := s.CurrentPeriodEnd.Time
		v.CurrentPeriodEnd = &t
	}
	writeJSON(w, http.StatusOK, v)
}

type checkoutReq struct {
	Plan string `json:"plan"`
}

// liveStatuses keep a paid plan in force. A second checkout while one
// of these holds would create a second subscription; the portal is
// where an existing payer changes plan.
var liveStatuses = map[string]bool{"active": true, "trialing": true, "past_due": true}

// Checkout creates a Stripe Checkout session for the requested plan
// and returns the redirect URL.
func (d *Deps) Checkout(w http.ResponseWriter, r *http.Request) {
	if !d.Stripe.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "billing_disabled", "billing is not configured")
		return
	}
	uid := middleware.GetUserID(r.Context())
	var req checkoutReq
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
		return
	}
	priceID := d.priceFor(req.Plan)
	if priceID == "" {
		writeErr(w, http.StatusUnprocessableEntity, "bad_plan", "")
		return
	}
	u, err := d.Users.ByID(r.Context(), uid)
	if err != nil {
		d.fail(w, r, "load account", err)
		return
	}
	s, _, err := d.subscription(r, uid)
	if err != nil {
		d.fail(w, r, "load subscription", err)
		return
	}
	if s.SubscriptionID.Valid && liveStatuses[s.Status] {
		writeErr(w, http.StatusConflict, "already_subscribed", "manage your plan from the billing portal")
		return
	}
	params := billingpkg.CheckoutParams{
		PriceID:    priceID,
		SuccessURL: d.WebURL + "/billing?billing=ok&session_id={CHECKOUT_SESSION_ID}",
		CancelURL:  d.WebURL + "/billing?billing=cancel",
		UserID:     uid,
	}
	if s.CustomerID.Valid {
		params.Customer = s.CustomerID.String
	} else {
		params.CustomerEmail = u.Email
	}
	sess, err := d.Stripe.CreateCheckoutSession(r.Context(), params)
	if err != nil {
		d.Logger.Error("billing: create checkout", "err", err)
		writeErr(w, http.StatusBadGateway, "stripe_failed", "the payment provider is unavailable; try again")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"checkout_url": sess.URL, "session_id": sess.ID})
}

// Portal opens the Stripe customer portal for a returning payer.
func (d *Deps) Portal(w http.ResponseWriter, r *http.Request) {
	if !d.Stripe.Configured() {
		writeErr(w, http.StatusServiceUnavailable, "billing_disabled", "billing is not configured")
		return
	}
	s, _, err := d.subscription(r, middleware.GetUserID(r.Context()))
	if err != nil {
		d.fail(w, r, "load subscription", err)
		return
	}
	if !s.CustomerID.Valid {
		writeErr(w, http.StatusConflict, "no_customer", "there is no billing account yet; choose a plan first")
		return
	}
	u, err := d.Stripe.CreatePortalSession(r.Context(), s.CustomerID.String, d.WebURL+"/billing")
	if err != nil {
		d.Logger.Error("billing: create portal", "err", err)
		writeErr(w, http.StatusBadGateway, "stripe_failed", "the payment provider is unavailable; try again")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"portal_url": u})
}

func (d *Deps) priceFor(plan string) string {
	switch plan {
	case billingpkg.PlanPro:
		return d.PriceIDProMonth
	case billingpkg.PlanFamily:
		return d.PriceIDFamilyMonth
	default:
		return ""
	}
}

// ServerUsage is one server's relayed bytes for a month.
type ServerUsage struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BytesIn  int64  `json:"bytes_in"`
	BytesOut int64  `json:"bytes_out"`
}

// Usage reports this month's relayed bytes for the caller's servers,
// against the plan's allowance. The relay's bandwidth meter flushes
// into bandwidth_monthly; this reads it.
func (d *Deps) Usage(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	u, err := d.Users.ByID(r.Context(), uid)
	if err != nil {
		d.fail(w, r, "load account", err)
		return
	}
	month := billingpkg.MonthStart(d.now())
	servers, used, err := AccountUsage(r.Context(), d.DB, uid, month)
	if err != nil {
		d.fail(w, r, "load usage", err)
		return
	}
	tier, ok := billingpkg.Tiers[u.Plan]
	if !ok {
		tier = billingpkg.Tiers[billingpkg.PlanFree]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"month":       month.Format("2006-01"),
		"plan":        u.Plan,
		"limit_bytes": tier.BandwidthBytesPerMo,
		"used_bytes":  used,
		"servers":     servers,
	})
}

// AccountUsage sums an account's relayed bytes for a month, per server.
// The admin console reads it too.
func AccountUsage(ctx context.Context, db *sql.DB, uid string, month time.Time) ([]ServerUsage, int64, error) {
	rows, err := db.QueryContext(ctx, `
        SELECT s.id, s.name, COALESCE(b.bytes_in,0), COALESCE(b.bytes_out,0)
        FROM servers s
        LEFT JOIN bandwidth_monthly b ON b.server_id = s.id AND b.month = $2
        WHERE s.owner_user_id = $1
        ORDER BY s.created_at
    `, uid, month)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []ServerUsage{}
	var total int64
	for rows.Next() {
		var s ServerUsage
		if err := rows.Scan(&s.ID, &s.Name, &s.BytesIn, &s.BytesOut); err != nil {
			return nil, 0, err
		}
		total += s.BytesIn + s.BytesOut
		out = append(out, s)
	}
	return out, total, rows.Err()
}

// EntitlementKey publishes the license-signing key: the public half
// release builds of the home embed (Epic 16 story 16.4).
func (d *Deps) EntitlementKey(w http.ResponseWriter, _ *http.Request) {
	if d.Entitlements == nil {
		writeErr(w, http.StatusServiceUnavailable, "entitlement_disabled", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"fingerprint": d.Entitlements.Fp,
		"public_key":  base64.StdEncoding.EncodeToString(d.Entitlements.Public()),
		"algorithm":   "ed25519",
	})
}

func (d *Deps) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	d.Logger.Error("billing: "+what, "err", err, "request_id", middleware.GetRequestID(r.Context()))
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
