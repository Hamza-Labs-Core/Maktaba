// Package billing (handlers) wires the billing HTTP surface:
//   POST /v1/billing/checkout    — start a Stripe checkout
//   POST /v1/billing/webhook     — receive Stripe webhooks
//   GET  /v1/billing/plans       — plan comparison
//   GET  /v1/billing/me          — current subscription state
package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

type Deps struct {
	DB             *sql.DB
	Stripe         *billingpkg.StripeClient
	Users          *stores.Users
	WebhookSecret  string
	PublicURL      string
	PriceIDProMonth    string
	PriceIDFamilyMonth string
}

func Mount(r interface {
	Get(pattern string, h http.HandlerFunc)
	Post(pattern string, h http.HandlerFunc)
}, d Deps) {
	r.Get("/v1/billing/plans", d.Plans)
	r.Get("/v1/billing/me", d.Me)
	r.Post("/v1/billing/checkout", d.Checkout)
	r.Post("/v1/billing/webhook", d.Webhook)
}

// Plans returns the static catalog from billing.Tiers. We deliberately
// surface a public-friendly subset and omit internal-only fields.
func (d *Deps) Plans(w http.ResponseWriter, r *http.Request) {
	type viewTier struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		PriceCents int    `json:"price_cents"`
		Bandwidth  int64  `json:"bandwidth_bytes_per_month"`
		MaxServers int    `json:"max_servers"`
		MaxStreams int    `json:"max_concurrent_streams"`
		RelayQoS   string `json:"relay_qos"`
		Transcoding bool  `json:"includes_transcoding"`
		FamilySeats int   `json:"family_seats"`
	}
	out := []viewTier{}
	for _, id := range []string{billingpkg.PlanFree, billingpkg.PlanPro, billingpkg.PlanFamily} {
		t := billingpkg.Tiers[id]
		out = append(out, viewTier{
			ID: t.ID, Name: t.Name, PriceCents: t.PricePerMonthCents,
			Bandwidth: t.BandwidthBytesPerMo, MaxServers: t.MaxServers,
			MaxStreams: t.MaxConcurrentStreams, RelayQoS: t.RelayQoS,
			Transcoding: t.IncludesTranscoding, FamilySeats: t.FamilySeats,
		})
	}
	writeJSON(w, 200, map[string]any{"plans": out})
}

type meView struct {
	Plan              string     `json:"plan"`
	Status            string     `json:"status"`
	CurrentPeriodEnd  *time.Time `json:"current_period_end,omitempty"`
	CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
}

func (d *Deps) Me(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	if uid == "" {
		writeErr(w, 401, "unauthorized", "")
		return
	}
	var v meView
	var end sql.NullTime
	err := d.DB.QueryRowContext(r.Context(), `
        SELECT plan, status, current_period_end, cancel_at_period_end
        FROM subscriptions WHERE user_id = $1
    `, uid).Scan(&v.Plan, &v.Status, &end, &v.CancelAtPeriodEnd)
	if errors.Is(err, sql.ErrNoRows) {
		v = meView{Plan: billingpkg.PlanFree, Status: "active"}
	} else if err != nil {
		writeErr(w, 500, "lookup_failed", err.Error())
		return
	}
	if end.Valid {
		t := end.Time
		v.CurrentPeriodEnd = &t
	}
	writeJSON(w, 200, v)
}

type checkoutReq struct {
	Plan string `json:"plan"`
}

// Checkout creates a Stripe Checkout session for the requested plan
// and returns the redirect URL. The SPA then navigates the browser
// there and Stripe drives the rest of the flow.
func (d *Deps) Checkout(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	if uid == "" {
		writeErr(w, 401, "unauthorized", "")
		return
	}
	var req checkoutReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid_body", err.Error())
		return
	}
	priceID := d.priceFor(req.Plan)
	if priceID == "" {
		writeErr(w, 422, "bad_plan", "")
		return
	}
	u, err := d.Users.ByID(r.Context(), uid)
	if err != nil {
		writeErr(w, 500, "lookup_failed", err.Error())
		return
	}
	success := d.PublicURL + "/account?billing=ok&session_id={CHECKOUT_SESSION_ID}"
	cancel := d.PublicURL + "/account?billing=cancel"
	sess, err := d.Stripe.CreateCheckoutSession(r.Context(), u.Email, priceID, success, cancel, uid)
	if err != nil {
		writeErr(w, 502, "stripe_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"checkout_url": sess.URL, "session_id": sess.ID})
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

// Webhook handles Stripe events. We verify the signature, dedupe by
// event id, then mutate the subscription state.
func (d *Deps) Webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, 400, "read_failed", err.Error())
		return
	}
	if _, err := billingpkg.VerifyWebhookSignature(body, r.Header.Get("Stripe-Signature"), d.WebhookSecret, time.Now(), 5*time.Minute); err != nil {
		writeErr(w, 400, "bad_signature", err.Error())
		return
	}
	var ev struct {
		ID   string          `json:"id"`
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		writeErr(w, 400, "bad_json", err.Error())
		return
	}
	// Dedupe via PK on stripe_events.event_id.
	if _, err := d.DB.ExecContext(r.Context(), `
        INSERT INTO stripe_events (event_id, type, payload) VALUES ($1,$2,$3)
        ON CONFLICT (event_id) DO NOTHING
    `, ev.ID, ev.Type, ev.Data); err != nil {
		writeErr(w, 500, "store_failed", err.Error())
		return
	}
	if err := d.applyEvent(r.Context(), ev.Type, ev.Data); err != nil {
		writeErr(w, 500, "apply_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// applyEvent is the small switch over the event types we care about.
// We deliberately ignore everything else — Stripe sends many events
// we don't need to act on, and silently 200ing them keeps the webhook
// retries from filling our error logs.
func (d *Deps) applyEvent(ctx context.Context, t string, raw json.RawMessage) error {
	type subShape struct {
		Object struct {
			ID                string `json:"id"`
			Customer          string `json:"customer"`
			Status            string `json:"status"`
			CancelAtPeriodEnd bool   `json:"cancel_at_period_end"`
			CurrentPeriodEnd  int64  `json:"current_period_end"`
			ClientReferenceID string `json:"client_reference_id"`
			Items             struct {
				Data []struct {
					Price struct {
						Lookup string `json:"lookup_key"`
						Nick   string `json:"nickname"`
					} `json:"price"`
				} `json:"data"`
			} `json:"items"`
			Metadata map[string]string `json:"metadata"`
		} `json:"object"`
	}
	switch t {
	case "checkout.session.completed",
		"customer.subscription.created",
		"customer.subscription.updated",
		"customer.subscription.deleted":
		var s subShape
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		uid := s.Object.ClientReferenceID
		if uid == "" {
			uid = s.Object.Metadata["user_id"]
		}
		if uid == "" {
			return nil // can't resolve user; let Stripe retry isn't useful here.
		}
		plan := planFromPriceNick(s.Object.Items.Data)
		_, err := d.DB.ExecContext(ctx, `
            INSERT INTO subscriptions (user_id, plan, stripe_customer_id, stripe_subscription_id, status, current_period_end, cancel_at_period_end, updated_at)
            VALUES ($1,$2,$3,$4,$5,to_timestamp($6),$7,now())
            ON CONFLICT (user_id) DO UPDATE SET
                plan = EXCLUDED.plan,
                stripe_customer_id = COALESCE(EXCLUDED.stripe_customer_id, subscriptions.stripe_customer_id),
                stripe_subscription_id = COALESCE(EXCLUDED.stripe_subscription_id, subscriptions.stripe_subscription_id),
                status = EXCLUDED.status,
                current_period_end = EXCLUDED.current_period_end,
                cancel_at_period_end = EXCLUDED.cancel_at_period_end,
                updated_at = now()
        `, uid, plan, s.Object.Customer, s.Object.ID, s.Object.Status, s.Object.CurrentPeriodEnd, s.Object.CancelAtPeriodEnd)
		if err != nil {
			return err
		}
		// Mirror the plan to users.plan so the access token's `plan`
		// claim is fresh on next refresh.
		_, _ = d.DB.ExecContext(ctx, `UPDATE users SET plan = $2 WHERE id = $1`, uid, plan)
	}
	return nil
}

func planFromPriceNick(items []struct {
	Price struct {
		Lookup string `json:"lookup_key"`
		Nick   string `json:"nickname"`
	} `json:"price"`
}) string {
	for _, it := range items {
		nick := it.Price.Lookup
		if nick == "" {
			nick = it.Price.Nick
		}
		switch nick {
		case "pro_monthly", "pro":
			return billingpkg.PlanPro
		case "family_monthly", "family":
			return billingpkg.PlanFamily
		}
	}
	return billingpkg.PlanFree
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": msg})
}
