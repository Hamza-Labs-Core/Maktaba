# Story 25.14 — Payment-provider webhook

> Epic 25 · Cloud relay · Phase 3 (billing)

## Description

The payment provider (TBD) is the system of record for billing state;
the cloud listens to its webhooks to keep `subscriptions`, `invoices`
and tier gates in sync. This story implements idempotent,
provider-neutral webhook handling.

Endpoint:

- `POST /v1/billing/webhook/{provider}` — public, no auth. Every
  delivery is handed to `billing.Provider.ParseWebhook`, which verifies
  it (signature, freshness) and translates it into a provider-neutral
  event. The route answers `404` unless `{provider}` is the configured
  provider and that provider takes payments. With the default provider,
  `none`, every webhook route is a 404.

Neutral events we handle:

| Event                     | What we do |
|---------------------------|-----|
| `checkout_completed`      | link the provider's customer id to the account |
| `subscription_started`    | upsert the subscription (plan, status, period end), set tier, NOTIFY `tier_changed` |
| `subscription_changed`    | update plan, status, period end, cancel-at-period-end |
| `subscription_cancelled`  | mark canceled; the account falls back to `free` |
| `payment_succeeded`       | record the invoice as paid; restore a `past_due` subscription |
| `payment_failed`          | record the invoice as failed; mark the subscription `past_due`; mail the payer |
| `payment_disputed`        | suspend the account; record a `chargeback` abuse signal |

Any other provider event comes back from `ParseWebhook` with no kind and
is acknowledged with `200` and no work.

Idempotency:

- Every event has the provider's unique event id. We insert into
  `provider_events` (primary key `(provider, provider_event_id)`) with
  `ON CONFLICT DO NOTHING`; if the row already exists, return 200 and
  do nothing. Provider retries with the same id are safe.
- Side effects (DB updates, NOTIFY) happen inside the same transaction
  as the `provider_events` insert, so a failed apply is retried by the
  provider.
- Billing rows carry the `provider` column that wrote them (`manual`
  for a plan a service admin set by hand).

## Acceptance criteria

- **Given** the provider is `none`,
  **when** anything is posted to `/v1/billing/webhook/{provider}`,
  **then** the response is `404`.
- **Given** a provider that takes payments sends `subscription_started`
  for user U on plan Pro,
  **when** the delivery verifies,
  **then** the response is `200`, `subscriptions` has the row,
  `provider_events` has the event id, and `NOTIFY tier_changed` fires.
- **Given** the provider retries the same event id,
  **when** the second delivery arrives,
  **then** the response is `200` and no duplicate work runs.
- **Given** a forged delivery,
  **when** verification fails,
  **then** the response is `400 invalid_signature` and a
  `billing_webhook_forgery` abuse signal is written.
- **Given** `payment_failed`,
  **when** processed,
  **then** the invoice is recorded as failed, the subscription is
  `past_due`, and the payer gets a "Your payment failed" email.
- **Given** `payment_disputed`,
  **when** processed,
  **then** the account is suspended and an abuse signal is written.
- **Given** an event older than the subscription's last applied event,
  **when** it arrives,
  **then** it is ignored.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | integration | provider `none` | post to webhook | 404 |
| T02 | integration | fake provider, bad signature | post | 400; `billing_webhook_forgery` signal |
| T03 | integration | replay same event | post twice | second is a no-op, 200 |
| T04 | integration | `subscription_started` | post | rows created, NOTIFY fired, plan mirrored onto servers |
| T05 | integration | `payment_failed` | post | invoice failed, `past_due`, email queued |
| T06 | integration | `payment_disputed` | post | account suspended |
| T07 | unit        | unknown provider event | post | 200, no-op |
| T08 | regression  | changed before started (out of order) | post both | final state correct; older event ignored |
| T09 | integration | webhook path names another provider | post | 404 |

## Edge cases

- **Out-of-order delivery.** Delivery order is not guaranteed. Each
  subscription event carries the full state and its timestamp; an event
  older than the last one applied is ignored.
- **Multiple subscriptions per user.** Out for v1: one active
  subscription per user. A stale "ghost" subscription surfaces in
  admin (25.20) for manual cleanup.
- **Customer email mismatch.** The provider's customer email may differ
  from our `users.email`. We don't sync; identity is our domain.
- **Failure during NOTIFY.** NOTIFY happens at COMMIT; if the
  transaction rolls back, no notify. The tier cache TTL is the safety
  net.
- **Effective plan.** Mirrored onto `users.plan` and onto every server
  the account owns, which is what the relay's bandwidth gate reads.

## Files / packages

- `cloud/internal/billing/provider.go` — `Provider.ParseWebhook`,
  neutral `Event` kinds.
- `cloud/internal/handlers/billing/webhook.go` — HTTP handler and event
  application.
- `cloud/migrations/00160001_billing_provider_neutral.sql` —
  `provider_*` columns, `provider_events` ledger.

## Open questions

- **Which payment provider.** TBD; its `ParseWebhook` decides the
  signature scheme and the mapping of its events to the neutral kinds.
- **Receipts in app.** The provider emails them; we may surface them
  in `/api/me`.
