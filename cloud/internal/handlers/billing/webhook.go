package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/abuse"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	mailpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// webhookTolerance is how old a signed event may be (replay window).
const webhookTolerance = 5 * time.Minute

type stripeEvent struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Created int64  `json:"created"`
	Data    struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

// Webhook handles Stripe events. The signature is verified first; the
// event is then recorded in stripe_events and applied in one
// transaction, so:
//   - a retried event id finds its row and is answered 200 with no work;
//   - a failed apply rolls the row back and answers 500, and Stripe's
//     retry gets a clean second attempt.
func (d *Deps) Webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read_failed", "")
		return
	}
	if d.WebhookSecret == "" {
		writeErr(w, http.StatusServiceUnavailable, "billing_disabled", "")
		return
	}
	if _, err := billingpkg.VerifyWebhookSignature(body, r.Header.Get("Stripe-Signature"), d.WebhookSecret, d.now(), webhookTolerance); err != nil {
		if d.Abuse != nil {
			if ip := middleware.GetClientIP(r.Context()); ip != "" {
				_ = d.Abuse.Record(r.Context(), abuse.Subject{Kind: "ip", ID: ip}, abuse.KindStripeForgery, 2, map[string]any{"reason": err.Error()})
			}
		}
		writeErr(w, http.StatusBadRequest, "invalid_signature", "")
		return
	}
	var ev stripeEvent
	if err := json.Unmarshal(body, &ev); err != nil || ev.ID == "" {
		writeErr(w, http.StatusBadRequest, "bad_json", "")
		return
	}
	tx, err := d.DB.BeginTx(r.Context(), nil)
	if err != nil {
		d.fail(w, r, "webhook begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	// A concurrent delivery of the same id blocks here on the primary
	// key until the first commits, then sees the conflict.
	res, err := tx.ExecContext(r.Context(), `
        INSERT INTO stripe_events (event_id, type, payload, applied_at) VALUES ($1,$2,$3, now())
        ON CONFLICT (event_id) DO NOTHING
    `, ev.ID, ev.Type, []byte(ev.Data.Object))
	if err != nil {
		d.fail(w, r, "webhook record", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		w.WriteHeader(http.StatusOK) // duplicate delivery
		return
	}
	notices, err := d.applyEvent(r.Context(), tx, ev)
	if err != nil {
		d.Logger.Error("billing: apply webhook", "event", ev.ID, "type", ev.Type, "err", err)
		writeErr(w, http.StatusInternalServerError, "apply_failed", "")
		return
	}
	if err := tx.Commit(); err != nil {
		d.fail(w, r, "webhook commit", err)
		return
	}
	// Side effects outside the database go after the commit, so a
	// rolled-back event never mails anyone or signs anyone out.
	for _, n := range notices {
		n(r.Context())
	}
	w.WriteHeader(http.StatusOK)
}

// notice is a post-commit side effect (mail, session revocation).
type notice func(ctx context.Context)

type subscriptionObject struct {
	ID                string            `json:"id"`
	Customer          string            `json:"customer"`
	Status            string            `json:"status"`
	CancelAtPeriodEnd bool              `json:"cancel_at_period_end"`
	CurrentPeriodEnd  int64             `json:"current_period_end"`
	Metadata          map[string]string `json:"metadata"`
	Items             struct {
		Data []struct {
			CurrentPeriodEnd int64 `json:"current_period_end"`
			Price            struct {
				ID        string `json:"id"`
				LookupKey string `json:"lookup_key"`
				Nickname  string `json:"nickname"`
				Recurring struct {
					Interval string `json:"interval"`
				} `json:"recurring"`
			} `json:"price"`
		} `json:"data"`
	} `json:"items"`
}

func (d *Deps) applyEvent(ctx context.Context, tx *sql.Tx, ev stripeEvent) ([]notice, error) {
	at := time.Unix(ev.Created, 0).UTC()
	switch ev.Type {
	case "checkout.session.completed":
		var o struct {
			Customer          string            `json:"customer"`
			Subscription      string            `json:"subscription"`
			ClientReferenceID string            `json:"client_reference_id"`
			Metadata          map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(ev.Data.Object, &o); err != nil {
			return nil, err
		}
		uid := firstNonEmpty(o.ClientReferenceID, o.Metadata["user_id"])
		if ok, err := userExists(ctx, tx, uid); err != nil || !ok || o.Customer == "" {
			return nil, err
		}
		// Link the payer; plan and status arrive with the subscription
		// events, which may come before or after this one.
		_, err := tx.ExecContext(ctx, `
            INSERT INTO subscriptions (user_id, stripe_customer_id, stripe_subscription_id, status, updated_at)
            VALUES ($1, $2, NULLIF($3,''), 'incomplete', now())
            ON CONFLICT (user_id) DO UPDATE SET
                stripe_customer_id = EXCLUDED.stripe_customer_id,
                stripe_subscription_id = COALESCE(EXCLUDED.stripe_subscription_id, subscriptions.stripe_subscription_id),
                updated_at = now()
        `, uid, o.Customer, o.Subscription)
		return nil, err

	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
		var o subscriptionObject
		if err := json.Unmarshal(ev.Data.Object, &o); err != nil {
			return nil, err
		}
		uid, err := d.resolveUser(ctx, tx, o.Metadata["user_id"], o.Customer)
		if err != nil || uid == "" {
			return nil, err
		}
		var cur struct {
			subID  sql.NullString
			status sql.NullString
		}
		err = tx.QueryRowContext(ctx, `SELECT stripe_subscription_id, status FROM subscriptions WHERE user_id = $1 FOR UPDATE`, uid).Scan(&cur.subID, &cur.status)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		status := o.Status
		if ev.Type == "customer.subscription.deleted" {
			status = "canceled"
		}
		// A second subscription (two checkouts paid) must not move the
		// account while the current one is live: only a live
		// subscription may take over from another.
		if cur.subID.Valid && cur.subID.String != o.ID && liveStatuses[cur.status.String] && !liveStatuses[status] {
			return nil, nil
		}
		tier, priceID, interval := d.planOf(o)
		effective := billingpkg.PlanFree
		if liveStatuses[status] {
			effective = tier
		}
		periodEnd := o.CurrentPeriodEnd
		if periodEnd == 0 && len(o.Items.Data) > 0 {
			periodEnd = o.Items.Data[0].CurrentPeriodEnd // newer API versions
		}
		var end any
		if periodEnd > 0 {
			end = time.Unix(periodEnd, 0).UTC()
		}
		// The ordering guard lives in the upsert itself, so two concurrent
		// deliveries for an account with no row yet cannot both win. Stripe
		// timestamps are whole seconds; on a tie, an "incomplete" event
		// never overwrites a live status (created → active usually share
		// a second and may arrive in either order).
		res, err := tx.ExecContext(ctx, `
            INSERT INTO subscriptions (user_id, plan, interval, stripe_customer_id, stripe_subscription_id, stripe_price_id,
                                       status, current_period_end, cancel_at_period_end, last_event_at, updated_at)
            VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),$5,NULLIF($6,''),$7,$8,$9,$10,now())
            ON CONFLICT (user_id) DO UPDATE SET
                plan = EXCLUDED.plan,
                interval = EXCLUDED.interval,
                stripe_customer_id = COALESCE(EXCLUDED.stripe_customer_id, subscriptions.stripe_customer_id),
                stripe_subscription_id = EXCLUDED.stripe_subscription_id,
                stripe_price_id = EXCLUDED.stripe_price_id,
                status = EXCLUDED.status,
                current_period_end = EXCLUDED.current_period_end,
                cancel_at_period_end = EXCLUDED.cancel_at_period_end,
                last_event_at = EXCLUDED.last_event_at,
                updated_at = now()
            WHERE subscriptions.last_event_at IS NULL
               OR subscriptions.last_event_at < EXCLUDED.last_event_at
               OR (subscriptions.last_event_at = EXCLUDED.last_event_at
                   AND NOT (EXCLUDED.status IN ('incomplete','incomplete_expired')
                            AND subscriptions.status IN ('active','trialing','past_due')))
        `, uid, effective, interval, o.Customer, o.ID, priceID, status, end, o.CancelAtPeriodEnd, at)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, nil // an older event delivered late
		}
		return nil, setEffectivePlan(ctx, tx, uid, effective)

	case "invoice.paid", "invoice.payment_failed":
		var o struct {
			ID        string `json:"id"`
			Customer  string `json:"customer"`
			AmountDue int64  `json:"amount_due"`
			Currency  string `json:"currency"`
		}
		if err := json.Unmarshal(ev.Data.Object, &o); err != nil {
			return nil, err
		}
		uid, err := d.resolveUser(ctx, tx, "", o.Customer)
		if err != nil || uid == "" {
			return nil, err
		}
		status := "paid"
		if ev.Type == "invoice.payment_failed" {
			status = "failed"
		}
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO invoices (stripe_invoice_id, user_id, status, amount_due_cents, currency, updated_at)
            VALUES ($1,$2,$3,$4,COALESCE(NULLIF($5,''),'usd'),now())
            ON CONFLICT (stripe_invoice_id) DO UPDATE SET status = EXCLUDED.status, updated_at = now()
        `, o.ID, uid, status, o.AmountDue, o.Currency); err != nil {
			return nil, err
		}
		if status != "failed" {
			return nil, nil
		}
		// Stripe keeps retrying; the subscription stays in force as
		// past_due until it gives up (customer.subscription.updated).
		if _, err := tx.ExecContext(ctx, `UPDATE subscriptions SET status = 'past_due', updated_at = now() WHERE user_id = $1 AND status = 'active'`, uid); err != nil {
			return nil, err
		}
		return []notice{d.mailUser(uid, "payment_failed", "Your Maktaba Cloud payment failed",
			"We could not charge your card for Maktaba Cloud. Update your payment method from the billing page: "+d.WebURL+"/billing")}, nil

	case "charge.dispute.created":
		var o struct {
			ID       string `json:"id"`
			Charge   string `json:"charge"`
			Customer string `json:"customer"`
		}
		if err := json.Unmarshal(ev.Data.Object, &o); err != nil {
			return nil, err
		}
		customer := o.Customer
		if customer == "" && o.Charge != "" && d.Stripe.Configured() {
			c, err := d.Stripe.ChargeCustomer(ctx, o.Charge)
			if err != nil {
				return nil, fmt.Errorf("resolve disputed charge: %w", err)
			}
			customer = c
		}
		uid, err := d.resolveUser(ctx, tx, "", customer)
		if err != nil || uid == "" {
			return nil, err
		}
		return d.suspendForDispute(ctx, tx, uid, o.ID)
	}
	return nil, nil
}

// suspendForDispute suspends the payer of a disputed charge (story
// 25.14): billing is left to Stripe, sessions go after the commit.
func (d *Deps) suspendForDispute(ctx context.Context, tx *sql.Tx, uid, disputeID string) ([]notice, error) {
	reason := "payment dispute " + disputeID
	if err := d.Users.SetStatus(ctx, tx, uid, stores.StatusSuspended, reason); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
        INSERT INTO abuse_signals (subject, subject_kind, kind, severity, detail)
        VALUES ($1, 'user', $2, 5, jsonb_build_object('dispute', $3::text))
    `, uid, abuse.KindDispute, disputeID); err != nil {
		return nil, err
	}
	if err := audit.WriteTx(ctx, tx, audit.Entry{
		ActorEmail: "stripe", Action: audit.ActionDisputeSuspend, TargetKind: "user", TargetID: uid,
		Detail: map[string]any{"dispute": disputeID},
	}); err != nil {
		return nil, err
	}
	return []notice{func(ctx context.Context) {
		if d.Sessions != nil {
			if err := d.Sessions.RevokeAllForUser(ctx, uid); err != nil {
				d.Logger.Error("billing: revoke sessions after dispute", "user_id", uid, "err", err)
			}
		}
	}}, nil
}

func (d *Deps) mailUser(uid, kind, subject, body string) notice {
	return func(ctx context.Context) {
		if d.Mailer == nil {
			return
		}
		u, err := d.Users.ByID(ctx, uid)
		if err != nil {
			d.Logger.Error("billing: mail lookup", "user_id", uid, "err", err)
			return
		}
		if err := d.Mailer.Send(ctx, mailpkg.Message{To: u.Email, Kind: kind, Subject: subject, Body: body}); err != nil {
			d.Logger.Error("billing: send mail", "kind", kind, "err", err)
		}
	}
}

// resolveUser finds the account an event is about: the user id we
// stamped on the subscription's metadata, else the account whose
// Stripe customer it is. "" (no error) means none — the event is about
// something we did not create, and is acknowledged without work.
func (d *Deps) resolveUser(ctx context.Context, tx *sql.Tx, metaUID, customer string) (string, error) {
	if ok, err := userExists(ctx, tx, metaUID); err != nil {
		return "", err
	} else if ok {
		return metaUID, nil
	}
	if customer == "" {
		return "", nil
	}
	var uid string
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM subscriptions WHERE stripe_customer_id = $1`, customer).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		d.Logger.Warn("billing: webhook for an unknown Stripe customer", "customer", customer)
		return "", nil
	}
	return uid, err
}

func userExists(ctx context.Context, tx *sql.Tx, uid string) (bool, error) {
	if uid == "" {
		return false, nil
	}
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id::text = $1)`, uid).Scan(&ok)
	return ok, err
}

// planOf maps a subscription's price to a tier: the configured price
// ids first, then the price's lookup_key or nickname
// (pro/pro_monthly/…). It also returns the price id and interval.
func (d *Deps) planOf(o subscriptionObject) (tier, priceID, interval string) {
	tier = billingpkg.PlanFree
	for _, it := range o.Items.Data {
		p := it.Price
		priceID = p.ID
		switch p.Recurring.Interval {
		case "month":
			interval = "monthly"
		case "year":
			interval = "yearly"
		}
		switch {
		case p.ID != "" && p.ID == d.PriceIDProMonth:
			return billingpkg.PlanPro, priceID, interval
		case p.ID != "" && p.ID == d.PriceIDFamilyMonth:
			return billingpkg.PlanFamily, priceID, interval
		}
		switch firstNonEmpty(p.LookupKey, p.Nickname) {
		case "pro", "pro_monthly", "pro_yearly":
			return billingpkg.PlanPro, priceID, interval
		case "family", "family_monthly", "family_yearly":
			return billingpkg.PlanFamily, priceID, interval
		}
	}
	return tier, priceID, interval
}

// setEffectivePlan mirrors the plan onto the account (the access
// token's plan claim) and onto every server it owns, which is what the
// relay's bandwidth gate reads.
func setEffectivePlan(ctx context.Context, tx *sql.Tx, uid, plan string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE users SET plan = $2, updated_at = now() WHERE id = $1`, uid, plan); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE servers SET plan = $2 WHERE owner_user_id = $1`, uid, plan)
	return err
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
