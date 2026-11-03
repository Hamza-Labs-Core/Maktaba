package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/abuse"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	mailpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Webhook handles a payment provider's events at
// /v1/billing/webhook/{provider}. Only the configured provider's route
// answers, and only when it takes payments: with the default "none"
// provider every webhook route is a 404.
//
// The provider verifies the delivery and translates it into a neutral
// billingpkg.Event. The event is then recorded in provider_events and
// applied in one transaction, so:
//   - a retried event id finds its row and is answered 200 with no work;
//   - a failed apply rolls the row back and answers 500, and the
//     provider's retry gets a clean second attempt.
func (d *Deps) Webhook(w http.ResponseWriter, r *http.Request) {
	p := d.provider()
	if !p.Enabled() || chi.URLParam(r, "provider") != p.Name() {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read_failed", "")
		return
	}
	ev, err := p.ParseWebhook(r.Context(), body, r.Header)
	if errors.Is(err, billingpkg.ErrBadSignature) {
		if d.Abuse != nil {
			if ip := middleware.GetClientIP(r.Context()); ip != "" {
				_ = d.Abuse.Record(r.Context(), abuse.Subject{Kind: "ip", ID: ip}, abuse.KindWebhookForgery, 2,
					map[string]any{"provider": p.Name(), "reason": err.Error()})
			}
		}
		writeErr(w, http.StatusBadRequest, "invalid_signature", "")
		return
	}
	if err != nil || ev.ID == "" {
		writeErr(w, http.StatusBadRequest, "bad_event", "")
		return
	}
	payload := ev.Payload
	if !json.Valid(payload) {
		payload = []byte("{}")
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
        INSERT INTO provider_events (provider, provider_event_id, type, payload, applied_at) VALUES ($1,$2,$3,$4, now())
        ON CONFLICT (provider, provider_event_id) DO NOTHING
    `, p.Name(), ev.ID, firstNonEmpty(ev.Type, string(ev.Kind), "unknown"), payload)
	if err != nil {
		d.fail(w, r, "webhook record", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		w.WriteHeader(http.StatusOK) // duplicate delivery
		return
	}
	notices, err := d.applyEvent(r.Context(), tx, p.Name(), ev)
	if err != nil {
		d.Logger.Error("billing: apply webhook", "provider", p.Name(), "event", ev.ID, "kind", ev.Kind, "err", err)
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

func (d *Deps) applyEvent(ctx context.Context, tx *sql.Tx, provider string, ev billingpkg.Event) ([]notice, error) {
	at := ev.At.UTC()
	switch ev.Kind {
	case billingpkg.EventCheckoutCompleted:
		uid := ev.UserID
		if ok, err := userExists(ctx, tx, uid); err != nil || !ok || ev.CustomerID == "" {
			return nil, err
		}
		// Link the payer; plan and status arrive with the subscription
		// events, which may come before or after this one.
		_, err := tx.ExecContext(ctx, `
            INSERT INTO subscriptions (user_id, provider, provider_customer_id, provider_subscription_id, status, updated_at)
            VALUES ($1, $2, $3, NULLIF($4,''), 'incomplete', now())
            ON CONFLICT (user_id) DO UPDATE SET
                provider = EXCLUDED.provider,
                provider_customer_id = EXCLUDED.provider_customer_id,
                provider_subscription_id = COALESCE(EXCLUDED.provider_subscription_id, subscriptions.provider_subscription_id),
                updated_at = now()
        `, uid, provider, ev.CustomerID, ev.SubscriptionID)
		return nil, err

	case billingpkg.EventSubscriptionStarted, billingpkg.EventSubscriptionChanged, billingpkg.EventSubscriptionCancelled:
		uid, err := d.resolveUser(ctx, tx, ev.UserID, ev.CustomerID)
		if err != nil || uid == "" {
			return nil, err
		}
		var cur struct {
			subID  sql.NullString
			status sql.NullString
		}
		err = tx.QueryRowContext(ctx, `SELECT provider_subscription_id, status FROM subscriptions WHERE user_id = $1 FOR UPDATE`, uid).Scan(&cur.subID, &cur.status)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		status := ev.Status
		if ev.Kind == billingpkg.EventSubscriptionCancelled {
			status = "canceled"
		}
		// A second subscription (two checkouts paid) must not move the
		// account while the current one is live: only a live
		// subscription may take over from another.
		if cur.subID.Valid && cur.subID.String != ev.SubscriptionID && liveStatuses[cur.status.String] && !liveStatuses[status] {
			return nil, nil
		}
		tier := ev.Plan
		if _, ok := billingpkg.Tiers[tier]; !ok {
			tier = billingpkg.PlanFree
		}
		effective := billingpkg.PlanFree
		if liveStatuses[status] {
			effective = tier
		}
		var end any
		if !ev.PeriodEnd.IsZero() {
			end = ev.PeriodEnd.UTC()
		}
		// The ordering guard lives in the upsert itself, so two concurrent
		// deliveries for an account with no row yet cannot both win. On a
		// timestamp tie, an "incomplete" event never overwrites a live
		// status (started → active often share a second and may arrive in
		// either order).
		res, err := tx.ExecContext(ctx, `
            INSERT INTO subscriptions (user_id, provider, plan, interval, provider_customer_id, provider_subscription_id, provider_price_id,
                                       status, current_period_end, cancel_at_period_end, last_event_at, updated_at)
            VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,NULLIF($7,''),$8,$9,$10,$11,now())
            ON CONFLICT (user_id) DO UPDATE SET
                provider = EXCLUDED.provider,
                plan = EXCLUDED.plan,
                interval = EXCLUDED.interval,
                provider_customer_id = COALESCE(EXCLUDED.provider_customer_id, subscriptions.provider_customer_id),
                provider_subscription_id = EXCLUDED.provider_subscription_id,
                provider_price_id = EXCLUDED.provider_price_id,
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
        `, uid, provider, effective, ev.Interval, ev.CustomerID, ev.SubscriptionID, ev.PriceID, status, end, ev.CancelAtPeriodEnd, at)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, nil // an older event delivered late
		}
		return nil, SetEffectivePlan(ctx, tx, uid, effective)

	case billingpkg.EventPaymentSucceeded, billingpkg.EventPaymentFailed:
		uid, err := d.resolveUser(ctx, tx, ev.UserID, ev.CustomerID)
		if err != nil || uid == "" || ev.InvoiceID == "" {
			return nil, err
		}
		status := "paid"
		if ev.Kind == billingpkg.EventPaymentFailed {
			status = "failed"
		}
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO invoices (provider, provider_invoice_id, user_id, status, amount_due_cents, currency, updated_at)
            VALUES ($1,$2,$3,$4,$5,COALESCE(NULLIF($6,''),'usd'),now())
            ON CONFLICT (provider_invoice_id) DO UPDATE SET status = EXCLUDED.status, updated_at = now()
        `, provider, ev.InvoiceID, uid, status, ev.AmountCents, ev.Currency); err != nil {
			return nil, err
		}
		if status != "failed" {
			return nil, nil
		}
		// The provider keeps retrying; the subscription stays in force as
		// past_due until it gives up (a subscription event follows).
		if _, err := tx.ExecContext(ctx, `UPDATE subscriptions SET status = 'past_due', updated_at = now() WHERE user_id = $1 AND status = 'active'`, uid); err != nil {
			return nil, err
		}
		return []notice{d.mailUser(uid, "payment_failed", "Your Maktaba Cloud payment failed",
			"We could not take your payment for Maktaba Cloud. Update your payment method from the billing page: "+d.WebURL+"/billing")}, nil

	case billingpkg.EventPaymentDisputed:
		uid, err := d.resolveUser(ctx, tx, ev.UserID, ev.CustomerID)
		if err != nil || uid == "" {
			return nil, err
		}
		return d.suspendForDispute(ctx, tx, provider, uid, ev.DisputeID)
	}
	return nil, nil
}

// suspendForDispute suspends the payer of a disputed payment (story
// 25.14): billing is left to the provider, sessions go after the commit.
func (d *Deps) suspendForDispute(ctx context.Context, tx *sql.Tx, provider, uid, disputeID string) ([]notice, error) {
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
		ActorEmail: "billing:" + provider, Action: audit.ActionDisputeSuspend, TargetKind: "user", TargetID: uid,
		Detail: map[string]any{"dispute": disputeID, "provider": provider},
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

// resolveUser finds the account an event is about: the user id the
// provider carried back, else the account whose provider customer it
// is. "" (no error) means none — the event is about something we did
// not create, and is acknowledged without work.
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
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM subscriptions WHERE provider_customer_id = $1`, customer).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		d.Logger.Warn("billing: webhook for an unknown provider customer", "customer", customer)
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

// SetEffectivePlan mirrors the plan onto the account (the access
// token's plan claim) and onto every server it owns, which is what the
// relay's bandwidth gate reads. The admin console's manual plan change
// uses it too.
func SetEffectivePlan(ctx context.Context, tx *sql.Tx, uid, plan string) error {
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
