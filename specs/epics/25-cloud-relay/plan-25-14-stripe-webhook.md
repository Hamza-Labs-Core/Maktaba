# Implementation Plan — Story 25.14 Stripe webhook handler

> Companion to [story-25-14-stripe-webhook.md](story-25-14-stripe-webhook.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Endpoint | `POST /api/billing/webhook` — public, no JWT auth, **signature-verified** via Stripe's `webhook.ConstructEvent`. |
| Where it runs | Both api role (for receipt) and worker role (for async processing). API role does signature check + idempotency insert + cheap state updates synchronously; expensive work goes to worker via outbox. |
| Idempotency | `stripe_events.stripe_event_id PK` (table from 25.13) with `ON CONFLICT DO NOTHING`. Side effects share the txn. |
| Notify | After commit, `pg_notify('tier_changed', user_id)` invalidates LRU caches across pods (25.12). |
| Reconciliation cron | Nightly: list active subs, compare with Stripe, fix drift. Bounded to 1000 calls/min. |
| Out of scope | UI changes (25.15). |

## 1. Handler

```go
// cloud/internal/billing/webhook.go
func (s *Service) Webhook(secret string) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
        if err != nil { problem(w, 400, "bad_request", ""); return }
        sig := r.Header.Get("Stripe-Signature")
        // Two webhook secrets active during rotation:
        var ev stripe.Event
        for _, sec := range s.secrets.Active(r.Context()) {
            ev, err = webhook.ConstructEvent(body, sig, sec)
            if err == nil { break }
        }
        if err != nil {
            s.abuse.Record(r.Context(), "stripe_signature_forgery", nil, 4)
            problem(w, 400, "invalid_signature", ""); return
        }
        if err := s.processEvent(r.Context(), &ev, body); err != nil {
            // Surface 503 so Stripe retries; we keep partial state in DB though.
            problem(w, 503, "internal", ""); return
        }
        w.WriteHeader(200)
    }
}
```

`s.secrets.Active(ctx)` returns up to 2 keys (current + previous) from the `stripe_secrets` table (declared in plan-25-13's `00060001_billing.sql`); rotation runbook documented in `docs/operations/stripe-webhook-secret-rotation.md`.

## 2. Process event

```go
func (s *Service) processEvent(ctx context.Context, ev *stripe.Event, raw []byte) error {
    // Strip PII before storage
    cleaned := stripPCI(raw)
    tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
    if err != nil { return err }
    defer tx.Rollback(ctx)
    res, err := tx.Exec(ctx, `
        INSERT INTO stripe_events(stripe_event_id, type, payload, received_at)
        VALUES ($1, $2, $3, now())
        ON CONFLICT (stripe_event_id) DO NOTHING
    `, ev.ID, ev.Type, cleaned)
    if err != nil { return err }
    if res.RowsAffected() == 0 {
        // already processed (or in flight); commit & return.
        return tx.Commit(ctx)
    }

    var userID uuid.UUID
    switch ev.Type {
    case "customer.created":
        userID, _ = s.applyCustomerCreated(ctx, tx, ev)
    case "customer.subscription.created", "customer.subscription.updated":
        userID, _ = s.applySubscription(ctx, tx, ev)
    case "customer.subscription.deleted":
        userID, _ = s.applySubscriptionDeleted(ctx, tx, ev)
    case "invoice.created":
        s.applyInvoiceCreated(ctx, tx, ev)
    case "invoice.paid":
        userID, _ = s.applyInvoicePaid(ctx, tx, ev)
    case "invoice.payment_failed":
        userID, _ = s.applyInvoiceFailed(ctx, tx, ev)
    case "invoice.finalized":
        s.applyInvoiceFinalized(ctx, tx, ev)
    case "charge.dispute.created":
        userID, _ = s.applyDispute(ctx, tx, ev)
    case "customer.deleted":
        userID, _ = s.applyCustomerDeleted(ctx, tx, ev)
    default:
        // Unknown / ignored.
        _, _ = tx.Exec(ctx, `UPDATE stripe_events SET processed_at = now() WHERE stripe_event_id=$1`, ev.ID)
        return tx.Commit(ctx)
    }

    if _, err := tx.Exec(ctx, `UPDATE stripe_events SET processed_at = now() WHERE stripe_event_id=$1`, ev.ID); err != nil {
        return err
    }
    if userID != uuid.Nil {
        _, _ = tx.Exec(ctx, `SELECT pg_notify('tier_changed', $1::text)`, userID.String())
    }
    return tx.Commit(ctx)
}
```

## 3. Apply functions

### 3.1 `applySubscription` (created/updated)

```go
func (s *Service) applySubscription(ctx context.Context, tx pgx.Tx, ev *stripe.Event) (uuid.UUID, error) {
    var sub stripe.Subscription
    if err := json.Unmarshal(ev.Data.Raw, &sub); err != nil { return uuid.Nil, err }
    customerID := sub.Customer.ID
    userID, err := s.repo.UserByCustomerID(ctx, tx, customerID)
    if err != nil { return uuid.Nil, err }
    plan := tierAndInterval(sub.Items.Data[0].Price.ID)

    // Out-of-order guard: only apply if event ts >= our last_event_at.
    eventTS := time.Unix(ev.Created, 0)
    var existingTS time.Time
    _ = tx.QueryRow(ctx, `SELECT last_event_at FROM subscriptions WHERE stripe_subscription_id=$1`, sub.ID).Scan(&existingTS)
    if !existingTS.IsZero() && eventTS.Before(existingTS) {
        return userID, nil
    }
    _, err = tx.Exec(ctx, `
        INSERT INTO subscriptions
            (user_id, stripe_subscription_id, stripe_customer_id, plan, status,
             current_period_start, current_period_end, cancel_at, last_event_at, metadata)
        VALUES ($1,$2,$3,$4,$5, to_timestamp($6), to_timestamp($7), to_timestamp(NULLIF($8,0)), $9, $10)
        ON CONFLICT (stripe_subscription_id) DO UPDATE SET
            plan = EXCLUDED.plan,
            status = EXCLUDED.status,
            current_period_start = EXCLUDED.current_period_start,
            current_period_end = EXCLUDED.current_period_end,
            cancel_at = EXCLUDED.cancel_at,
            last_event_at = EXCLUDED.last_event_at,
            metadata = EXCLUDED.metadata
    `, userID, sub.ID, customerID, plan, string(sub.Status),
        sub.CurrentPeriodStart, sub.CurrentPeriodEnd,
        cancelOrZero(sub.CancelAt), eventTS, metadataJSON(sub.Metadata))
    return userID, err
}
```

### 3.2 `applyInvoiceFailed`

```go
func (s *Service) applyInvoiceFailed(ctx context.Context, tx pgx.Tx, ev *stripe.Event) (uuid.UUID, error) {
    var inv stripe.Invoice
    json.Unmarshal(ev.Data.Raw, &inv)
    userID, _ := s.repo.UserByCustomerID(ctx, tx, inv.Customer.ID)
    _, err := tx.Exec(ctx, `
        INSERT INTO invoices(stripe_invoice_id, user_id, total_cents, currency, status, period_start, period_end)
        VALUES ($1,$2,$3,$4,'failed', to_timestamp($5), to_timestamp($6))
        ON CONFLICT (stripe_invoice_id) DO UPDATE SET status='failed'
    `, inv.ID, userID, inv.AmountDue, inv.Currency, inv.PeriodStart, inv.PeriodEnd)
    if err != nil { return userID, err }
    // Send "your payment failed" email + push (async, outbox).
    s.outbox.Enqueue(ctx, "billing.payment_failed", userID, inv.ID)
    return userID, nil
}
```

### 3.3 `applyDispute`

```go
func (s *Service) applyDispute(ctx context.Context, tx pgx.Tx, ev *stripe.Event) (uuid.UUID, error) {
    var d stripe.Dispute
    json.Unmarshal(ev.Data.Raw, &d)
    userID, _ := s.repo.UserByChargeID(ctx, tx, d.Charge.ID)
    _, err := tx.Exec(ctx, `UPDATE users SET suspended_at = now() WHERE id=$1`, userID)
    if err != nil { return userID, err }
    s.abuse.RecordTx(ctx, tx, "chargeback", &userID, 5)
    s.outbox.Enqueue(ctx, "support.dispute_alert", userID, d.ID)
    return userID, nil
}
```

## 4. PII stripping

```go
func stripPCI(raw []byte) []byte {
    // Keep payload but null out sensitive paths.
    var obj map[string]any
    if err := json.Unmarshal(raw, &obj); err != nil { return raw }
    stripPath(obj, "data", "object", "payment_method_details")
    stripPath(obj, "data", "object", "charges", "data", 0, "payment_method_details")
    stripPath(obj, "data", "object", "source", "card", "last4")
    stripPath(obj, "data", "object", "source", "card", "fingerprint")
    out, _ := json.Marshal(obj)
    return out
}
```

## 5. Reconciliation cron

```go
// cloud/internal/jobs/reconcile_billing.go
func Reconcile(ctx context.Context, db *pgxpool.Pool) error {
    rows, _ := db.Query(ctx, `SELECT user_id, stripe_subscription_id FROM subscriptions WHERE status IN ('active','past_due')`)
    for rows.Next() {
        var uid uuid.UUID; var sid string
        rows.Scan(&uid, &sid)
        sub, err := subscription.Get(sid, nil)
        if err != nil { continue }
        plan := tierAndInterval(sub.Items.Data[0].Price.ID)
        _, _ = db.Exec(ctx, `
            UPDATE subscriptions
            SET plan=$1, status=$2,
                current_period_end=to_timestamp($3), cancel_at=to_timestamp(NULLIF($4,0)),
                last_event_at=GREATEST(last_event_at, now())
            WHERE stripe_subscription_id=$5
        `, plan, string(sub.Status), sub.CurrentPeriodEnd, cancelOrZero(sub.CancelAt), sid)
        _, _ = db.Exec(ctx, `SELECT pg_notify('tier_changed', $1::text)`, uid.String())
        // Respect Stripe rate limits: 1000 calls/min.
        time.Sleep(60 * time.Millisecond)
    }
    return rows.Err()
}
```

Schedule: `0 4 * * *` (04:00 UTC daily) via cron in worker role.

Also: 7-day `past_due` downgrade cron:

```go
func DowngradePastDue(ctx context.Context, db *pgxpool.Pool) error {
    _, err := db.Exec(ctx, `
        UPDATE subscriptions
        SET plan='canceled_pending', status='canceled', last_event_at=now()
        WHERE status='past_due'
          AND last_event_at <= now() - INTERVAL '7 days'
        RETURNING user_id
    `)
    // … pg_notify each affected user_id …
    return err
}
```

## 6. Test plan

### 6.1 Unit

| Test | Pins |
|---|---|
| `TestSignatureVerifyOK` | Valid → accepted. |
| `TestSignatureMutationRejected` | Body tweak → 400 + abuse event. |
| `TestPriceIDMapping` | Each Stripe price ID maps to expected `Plan`. |
| `TestOutOfOrderEventGuard` | Older event ts → skipped. |
| `TestPCIStripping` | last4 + fingerprint removed; rest preserved. |

### 6.2 Integration

| Test | Pins |
|---|---|
| `TestSubscriptionCreatedFiresNotify` | Row exists; NOTIFY received; tier cache invalidated. |
| `TestReplaySameEventID` | Second delivery is 200 no-op. |
| `TestPaymentFailedThen7DayDowngrade` | Mark failed; cron downgrades. |
| `TestDisputeSuspends` | User suspended; abuse + outbox. |
| `TestReconciliationFixesDrift` | Manually drift status; cron repairs. |
| `TestUnknownEventTypeIgnored` | 200, no rows. |
| `TestRateLimitedReconcile` | 1000 subs reconcile in ~1min. |

## 7. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Out-of-order delivery | TS guard; latest-wins. | `TestOutOfOrderEventGuard`. |
| Time-travel replay | TS guard absorbs. | Same. |
| Multiple subs per user | Surface in admin (25.20); manual cleanup. | Spec. |
| Customer email mismatch | We don't sync. | Spec. |
| Webhook secret rotation | Two active; previous retired 24h. | Doc. |
| Local test webhooks | `stripe listen --forward-to`. | Doc. |
| NOTIFY only on commit | Cache TTL 60s is safety net. | Spec. |
| Reconciliation rate | 60ms pacing → ~1000/min. | Implementation. |
| PII in payloads | Stripped before persist. | `TestPCIStripping`. |
| Webhook 10s budget | Sync work is cheap; expensive work via outbox. | Spec. |

## 8. Dependencies

- 25.1, 25.12, 25.13.
- 25.17 (push outbox for emails/notifications — `s.outbox.Enqueue`).
- 25.25 (abuse events — `chargeback`).

## 9. Acceptance checklist

- [ ] Webhook signature verified, two-secret rotation.
- [ ] `stripe_events` idempotency.
- [ ] All event types in story table handled.
- [ ] NOTIFY `tier_changed` after commit.
- [ ] Dispute suspends user; outbox alerts.
- [ ] PII strip before persist.
- [ ] Reconciliation cron daily.
- [ ] 7-day past_due → downgrade.
- [ ] Tests in §6 pass.
