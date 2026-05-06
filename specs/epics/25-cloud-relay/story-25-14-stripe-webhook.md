# Story 25.14 — Stripe webhook handler

> Epic 25 · Cloud relay · Phase 3 (billing)

## Description

Stripe is the system-of-record for billing state; the cloud listens to
its webhooks to keep `cloud_subscriptions`, `cloud_invoices`, and tier
gates in sync. This story implements idempotent webhook handling for
the events that materially affect users.

Endpoint:

- `POST /api/billing/webhook` — public, no auth, but every request
  is verified against the Stripe-signed `Stripe-Signature` header
  using `stripe-go/webhook.ConstructEvent` and the configured
  `webhook_secret`.

Events we handle:

| Stripe event                       | What we do |
|------------------------------------|-----|
| `customer.created`                 | persist `stripe_customer_id` if not already on the user |
| `customer.subscription.created`    | insert `cloud_subscriptions`, set tier, NOTIFY `tier_changed` |
| `customer.subscription.updated`    | update plan, status, `current_period_end`, `cancel_at` |
| `customer.subscription.deleted`    | mark canceled, downgrade to `free` after `current_period_end` |
| `invoice.created`                  | insert `cloud_invoices` |
| `invoice.paid`                     | mark invoice paid; if status was `past_due`, restore tier |
| `invoice.payment_failed`           | mark invoice failed; after 7 days `past_due`, downgrade |
| `invoice.finalized`                | finalize totals; rare race condition on `paid` |
| `charge.dispute.created`           | record `cloud_abuse_events kind=chargeback`; suspend user |
| `customer.deleted`                 | rare; clear `stripe_customer_id`; user remains on free |

Events we ignore (no-op, return 200):

- `payment_method.*`
- `charge.refunded` (we observe via invoice flow only)
- `customer.tax_id.*`
- `payment_intent.*`
- All `*.preview.*`

Idempotency:

- Every event has a unique `id` (e.g., `evt_1OabcXyZ...`). We
  insert into `cloud_webhook_events(stripe_event_id PK,
  processed_at, payload_jsonb)` with `ON CONFLICT DO NOTHING` —
  if the row already exists, return 200 immediately and do
  nothing. Stripe retries with the same id; this is safe.
- Side effects (DB updates, NOTIFY) happen inside the same
  transaction as the `cloud_webhook_events` insert.

Failure modes:

- Stripe expects a 2xx within 10s. If we time out or 5xx,
  Stripe retries with backoff for up to 3 days. We must
  finish work within budget; long jobs are queued via
  `cloud_push_outbox`-style pattern (e.g., issuing
  entitlements is async).

## Acceptance criteria

- **Given** Stripe sends `customer.subscription.created` for
  user U on plan Pro,
  **when** the webhook arrives with a valid signature,
  **then** the response is `200`, `cloud_subscriptions` has
  the row, `cloud_webhook_events` has the event id, and
  Postgres `NOTIFY tier_changed user_id=...` fires.
- **Given** Stripe retries the same event id,
  **when** the second delivery arrives,
  **then** the response is `200`, no duplicate work runs,
  no new rows created.
- **Given** an attacker posts a forged event,
  **when** signature verification fails,
  **then** the response is `400 invalid_signature` and
  `cloud_abuse_events kind=stripe_signature_forgery` is
  written.
- **Given** `invoice.payment_failed` fires,
  **when** processed,
  **then** `cloud_invoices.status='failed'` is set and the
  user gets an email "Your payment failed".
- **Given** `cloud_invoices.status='failed'` for 7 days,
  **when** the daily reconciliation cron runs,
  **then** the user is downgraded to free, an email goes
  out, and audit row recorded.
- **Given** `charge.dispute.created` fires,
  **when** processed,
  **then** the user is immediately suspended (`cloud_users.suspended_at`),
  abuse event written, support email queued.
- **Given** the webhook handler is slow (DB lock),
  **when** processing exceeds 5s,
  **then** the work is offloaded to a background worker;
  the webhook returns 200 within 1s; reconciliation
  catches missed updates nightly.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | valid signature | verify | accepted |
| T02 | unit        | mutated payload | verify | rejected |
| T03 | integration | replay same event | post twice | second is no-op, 200 |
| T04 | integration | subscription.created | post | rows created, NOTIFY fired, tier cache invalidated |
| T05 | integration | payment_failed → 7 days → reconcile | run cron | downgrade applied |
| T06 | integration | dispute.created | post | user suspended, server tunnels reject |
| T07 | regression  | webhook arrives during DB outage | retry behavior | 503; Stripe retries; eventually applied |
| T08 | unit        | unknown event type | post | 200, no-op |
| T09 | regression  | event ordering: updated before created | post both out of order | final state correct (we trust each event independently) |
| T10 | integration | nightly reconciliation | mismatch with Stripe | reconcile re-syncs |

## Edge cases

- **Out-of-order delivery.** Stripe doesn't guarantee order.
  Our `customer.subscription.updated` handler unconditionally
  upserts the latest state from the event payload (which
  includes the full subscription object), so order doesn't
  matter as long as we honor the latest `updated` event.
  We check `event.created` timestamp and refuse to apply
  an older event over a newer state we already saw.
- **Time-travel events.** Stripe replays events during
  partial outages; the timestamp check handles it.
- **Multiple subscriptions per user.** Out for v1: one
  active subscription per user. If a stale "ghost"
  subscription appears, we surface it in admin (25.20) for
  manual cleanup.
- **Customer email mismatch.** Stripe customer may carry an
  email that no longer matches our `cloud_users.email`.
  We don't sync — Stripe is for billing; user identity
  is our domain.
- **Webhook secret rotation.** Two secrets active at once
  (current + previous), both attempted on verify; previous
  retired after 24h. Document rotation runbook.
- **Local test webhooks.** `stripe listen --forward-to
  localhost:8080/api/billing/webhook` for dev. Document.
- **Failure during NOTIFY.** NOTIFY happens at COMMIT;
  if the txn rolls back, no notify. The 60s LRU cache
  TTL is the safety net.
- **Reconciliation cron.** Every night, list `cloud_subscriptions`
  active rows, compare each with Stripe's state, fix
  drifts. Bounded to 1000 calls/min to respect rate limits.
- **PII in webhook payloads.** Stripe sends emails, last4
  PAN, etc. We strip `last4` and `card.fingerprint` before
  storing in `cloud_webhook_events.payload_jsonb`.

## Files / packages

- `cloud/internal/billing/webhook.go` — HTTP handler.
- `cloud/internal/billing/state.go` — apply event to DB.
- `cloud/internal/jobs/reconcile_billing.go` — nightly.
- `cloud/internal/billing/notify.go` — `pg_notify` helper.
- `cloud/migrations/00040004_billing.sql` —
  `cloud_subscriptions`, `cloud_invoices`,
  `cloud_webhook_events`.

## Open questions

- **Receipts in app.** Stripe emails them; we may surface
  in `/api/me`.
- **VAT invoices.** Stripe issues compliant invoices for
  EU; we link to them. v1 sufficient.
