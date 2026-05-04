# Implementation Plan — Story 16.3 Subscription management

> Companion to [story-16-03-subscription-management.md](story-16-03-subscription-management.md).
> The story states *what* and *why*; this plan states *how*.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Settings UI | `web/src/features/settings/Subscription.tsx` (canonical); native shells deep-link out to the same web view via embedded WebView so we don't duplicate the billing portal. |
| Stripe portal session minting | `api/internal/billing/portal.go` — `POST /api/billing/portal-session` returns a one-time `url` to Stripe Customer Portal. |
| Webhook receiver | `api/internal/billing/webhook.go` mounted at `POST /api/billing/webhook` — verifies Stripe signature; updates `licenses` table. |
| Reconciliation | `api/internal/billing/reconcile.go` — daily cron against Stripe API for source-of-truth. |
| Cancellation copy | Localized strings in `api/internal/i18n/locales/{en,ar}.toml` under `billing.cancel.*`. |
| Out of scope | Pricing decisions (open question in epic README); Apple/Google IAP (deferred). |

## 1. Architecture diagram

```
   user (admin)                     Stripe                       server
       │                              │                            │
       │ GET /api/billing/portal-session                            │
       │ ─────────────────────────────────────────────────────────► │
       │                                                            │  Stripe.PortalSessions.Create
       │                                                            │ ───────────► Stripe
       │ ◄────────────────────────── { url }                        │
       │                                                            │
       │ open url (Stripe-hosted)                                   │
       │ ─────────► Stripe                                          │
       │                                                            │
       │           change plan / cancel / update card               │
       │ ◄─────────                                                 │
       │                                                            │
       │                              │  webhook  customer.subscription.updated
       │                              │ ─────────────────────────► │
       │                                                            │  verify sig
       │                                                            │  upsert license row
       │                                                            │  bump tier
       │ flags reload (Story 16.6)                                  │
       │ ◄────────────────────────────────────────────────────────  │
```

## 2. Schema additions

`shared/db/migrations/0062_billing.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE billing_customers (
    user_id           UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    stripe_customer   TEXT NOT NULL UNIQUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE billing_subscriptions (
    stripe_subscription TEXT PRIMARY KEY,
    stripe_customer     TEXT NOT NULL,
    tier                TEXT NOT NULL CHECK (tier IN ('free','home','pro')),
    status              TEXT NOT NULL,           -- active, canceled, past_due, etc.
    current_period_end  TIMESTAMPTZ NOT NULL,
    cancel_at_period_end BOOLEAN NOT NULL DEFAULT false,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE billing_invoices (
    stripe_invoice    TEXT PRIMARY KEY,
    stripe_customer   TEXT NOT NULL,
    amount_due_cents  BIGINT NOT NULL,
    currency          TEXT NOT NULL,
    status            TEXT NOT NULL,
    pdf_url           TEXT,
    issued_at         TIMESTAMPTZ NOT NULL,
    paid_at           TIMESTAMPTZ
);

CREATE TABLE billing_webhook_events (
    stripe_event_id   TEXT PRIMARY KEY,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at      TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS billing_webhook_events;
DROP TABLE IF EXISTS billing_invoices;
DROP TABLE IF EXISTS billing_subscriptions;
DROP TABLE IF EXISTS billing_customers;
-- +goose StatementEnd
```

`billing_webhook_events` is the dedupe table — a Stripe webhook can be retried; the primary key prevents double-processing.

## 3. Portal session

```go
// api/internal/billing/portal.go
func (b *Service) CreatePortalSession(ctx context.Context, userID uuid.UUID) (string, error) {
    cust, err := b.ensureCustomer(ctx, userID)   // upsert
    if err != nil { return "", err }
    sess, err := b.stripe.BillingPortalSessions.Create(&stripe.BillingPortalSessionParams{
        Customer:  &cust.StripeCustomer,
        ReturnURL: stripe.String(b.cfg.AppOrigin + "/settings/subscription"),
    })
    if err != nil { return "", err }
    return sess.URL, nil
}
```

`ensureCustomer` looks up `billing_customers`; on miss, calls `Stripe.Customers.Create` with the user's email and inserts the row. Stripe customer creation is idempotent on email by Stripe's recommendation; we additionally guard at the DB layer.

## 4. Webhook handler

```go
// api/internal/billing/webhook.go
func (b *Service) HandleWebhook(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))   // 1 MB cap
    event, err := webhook.ConstructEvent(body, r.Header.Get("Stripe-Signature"), b.cfg.WebhookSecret)
    if err != nil { w.WriteHeader(400); return }

    // Dedupe.
    if _, err := b.db.InsertWebhookEvent(r.Context(), event.ID); err != nil {
        // duplicate
        w.WriteHeader(200); return
    }

    switch event.Type {
    case "customer.subscription.created", "customer.subscription.updated":
        var sub stripe.Subscription
        json.Unmarshal(event.Data.Raw, &sub)
        b.upsertSubscription(r.Context(), &sub)
    case "customer.subscription.deleted":
        var sub stripe.Subscription
        json.Unmarshal(event.Data.Raw, &sub)
        b.markCancelled(r.Context(), &sub)
    case "invoice.payment_succeeded":
        var inv stripe.Invoice
        json.Unmarshal(event.Data.Raw, &inv)
        b.upsertInvoice(r.Context(), &inv)
    case "charge.dispute.created":
        // Story EC: Disputed payment → tier flips to free until resolved.
        b.handleDispute(r.Context(), event)
    }
    b.db.MarkWebhookProcessed(r.Context(), event.ID)
    w.WriteHeader(200)
}
```

The handler updates `billing_subscriptions` and then **issues a fresh license** by calling the license-server (or the embedded license signer for self-hosted Maktaba editions). The license arrival flips the tier downstream (Story 16.4).

## 5. Reconciliation cron

`api/internal/billing/reconcile.go`:

```go
// Runs daily at 04:00 server local time.
func (b *Service) Reconcile(ctx context.Context) error {
    iter := b.stripe.Subscriptions.List(&stripe.SubscriptionListParams{Status: stripe.String("all")})
    for iter.Next() {
        sub := iter.Subscription()
        b.upsertSubscription(ctx, sub)
    }
    return iter.Err()
}
```

This catches webhook delivery failures: the AC says "we retry with backoff; reconcile via daily cron against Stripe's source of truth." Stripe's webhook retry already does the first pass; the daily reconcile is the safety net.

## 6. Cancellation flow (UX)

The Settings page calls Stripe via the portal; Stripe handles "downgrade takes effect on next renewal" semantically. Our local `billing_subscriptions.cancel_at_period_end = true` mirrors Stripe's flag. The UI reads:

```tsx
{sub.cancel_at_period_end && (
    <Banner intent="info">
        Cancels on {fmt(sub.current_period_end)}. <a onClick={resume}>Restore subscription</a>
    </Banner>
)}
```

`resume` calls `POST /api/billing/cancel?action=undo` which calls Stripe's `Subscriptions.Update(id, cancel_at_period_end=false)`. AC: "Restore a cancelled subscription before expiry: feature parity preserved."

## 7. Receipts & VAT

Invoices come from Stripe with `invoice_pdf` URL. We store the URL in `billing_invoices.pdf_url`. The Settings → Subscription page lists the last 12 invoices with download links.

VAT/tax: Stripe Tax (where the merchant has it configured) places VAT on the invoice line items; the invoice PDF shows it. We display `invoice.amount_paid` (post-tax) on the list, and the download link for the legal PDF — we do not parse line items.

## 8. Disputed payment

```go
func (b *Service) handleDispute(ctx context.Context, event stripe.Event) {
    var disp stripe.Dispute
    json.Unmarshal(event.Data.Raw, &disp)
    cust := disp.PaymentIntent.Customer.ID
    // Flip tier to free; preserve data per Story 16.1.
    sub, _ := b.db.GetActiveSubscriptionForCustomer(ctx, cust)
    b.db.UpdateSubscriptionTier(ctx, sub.StripeSubscription, "free")
    b.audit(ctx, "subscription-disputed", sub.StripeSubscription)
}
```

The AC says "License tier flips to `free` until resolved; no data is destroyed." Resolution comes via the next Stripe webhook (`charge.dispute.closed`).

## 9. Test plan

### 9.1 Portal

| Test | What it pins |
|---|---|
| `TestPortalSessionRequiresAuth` | Anon → 401. |
| `TestPortalSessionEnsuresCustomer` | First call inserts a `billing_customers` row; second reuses it. |
| `TestPortalSessionReturnsStripeURL` | Stub Stripe; response contains a `url`. |

### 9.2 Webhook

| Test | What it pins |
|---|---|
| `TestWebhookSignatureRequired` | Bad signature → 400. |
| `TestWebhookDedupesByEventID` | Same event twice → first processes, second 200 no-op. |
| `TestWebhookSubUpdatedUpsertsRow` | `customer.subscription.updated` → `billing_subscriptions` reflects new period_end. |
| `TestWebhookSubDeletedFlipsToFree` | `customer.subscription.deleted` → tier=free; license rotated. |
| `TestWebhookInvoiceAdded` | `invoice.payment_succeeded` → invoice row with `paid_at`. |
| `TestWebhookDisputeFlipsTier` | `charge.dispute.created` → `billing_subscriptions.tier = 'free'`. |

### 9.3 Reconcile

| Test | What it pins |
|---|---|
| `TestReconcileFillsMissedWebhook` | Stripe has sub state X; local has Y. After reconcile, local = X. |
| `TestReconcileNoOpWhenInSync` | Local matches Stripe; reconcile produces no DB writes. |

### 9.4 UI

| Test | What it pins |
|---|---|
| `testCancelBannerShows` | `cancel_at_period_end = true` → banner visible. |
| `testRestoreCancelsCancellation` | Click Restore → POST → `cancel_at_period_end = false`. |
| `testInvoiceListRendersLast12` | 15 invoices → first 12 shown. |
| `testUpgradeUIUnlocksWithin60s` | Stub webhook delivers `home → pro`; flag refresh happens within 60 s; UI shows new entries. |

## 10. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| Webhook delivery failure | Stripe retries; we also reconcile daily. | `TestReconcileFillsMissedWebhook` |
| Double-purchase (two devices) | Stripe customer dedupe by email; second purchase auto-refunded by Stripe; we just see two subs and pick the most recent active. | `TestDoublePurchaseHandled` |
| Disputed payment | Tier → free; data preserved; resolved on dispute close. | `TestWebhookDisputeFlipsTier` |
| User deleted in our DB but lingering Stripe customer | Cascade in our DB removes `billing_customers`; Stripe customer remains (we never delete from Stripe). Reconcile ignores customers without local row. | `TestUserDeleteCascadesBilling` |
| Receipt PDF requires re-auth at Stripe | The PDF URL is short-lived; we proxy through `GET /api/billing/invoices/{id}/pdf` that mints a fresh URL on demand. | `TestInvoicePDFProxyMintsFresh` |
| VAT change mid-renewal | Stripe issues a new invoice with the correct rate; we just persist it. | `TestVATInvoiceStored` |
| Webhook payload > 1 MB | Truncated by `io.LimitReader`; signature fails on truncated body → 400. Stripe retries; if persistent, manual triage. | `TestWebhookOversizeRejected` |
| Stripe customer email collision | If a user changes email and another user takes the old email, Stripe's customer search may match the wrong one. We index by `user_id ↔ stripe_customer` (PK), not email. | `TestEmailReuseSafe` |
| Multiple active subs per customer | Stripe shouldn't allow this normally; if it happens, we pick the highest tier. | `TestPickHighestTier` |

## 11. Acceptance checklist

**Schema**
- [ ] All four `billing_*` tables exist; cascades on `users.delete`.

**API**
- [ ] `POST /api/billing/portal-session` returns a Stripe URL.
- [ ] `POST /api/billing/webhook` validates signature, dedupes, updates state.
- [ ] `POST /api/billing/cancel?action=undo` works.

**UI**
- [ ] Subscription page shows tier, cancel banner, invoice list.

**Reconciliation**
- [ ] Daily cron registered.

**Tests**
- [ ] All §9 tests pass.

**Docs**
- [ ] `docs/operations/billing.md` covers webhook URL, signing secret rotation.
- [ ] `specs/epics/16-subscriptions/README.md` ticks story 16.3.
