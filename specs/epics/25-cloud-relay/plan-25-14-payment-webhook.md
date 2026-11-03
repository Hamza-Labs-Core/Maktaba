# Implementation Plan — Story 25.14 Payment-provider webhook

> Companion to [story-25-14-payment-webhook.md](story-25-14-payment-webhook.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Endpoint | `POST /v1/billing/webhook/{provider}` — public, no JWT auth. `404` unless `{provider}` equals the configured provider and that provider is enabled. Under the default provider `none`, always `404`. |
| Verification | `billing.Provider.ParseWebhook(ctx, payload, header)` verifies the delivery (signature, freshness) and returns a provider-neutral `billing.Event`. A failure wraps `billing.ErrBadSignature`. |
| Idempotency | `provider_events` PK `(provider, provider_event_id)` with `ON CONFLICT DO NOTHING`. Side effects share the transaction. |
| Notify | After commit, `pg_notify('tier_changed', user_id)` invalidates tier caches across pods (25.12). |
| Out of scope | UI changes (25.15). Choosing the provider (TBD). |

## 1. Handler

1. Read the body (1 MiB cap).
2. Route check: `{provider}` must be the configured provider and
   `Enabled()`; otherwise `404`.
3. `ParseWebhook`. On `ErrBadSignature`: record a
   `billing_webhook_forgery` abuse signal (25.25) and answer
   `400 invalid_signature`.
4. An event with an empty `Kind` (a provider event the cloud does not
   act on) is acknowledged with `200` and no work.
5. Apply the event (§2). On error answer `503` so the provider retries.

## 2. Applying an event

In one transaction:

- Insert into `provider_events(provider, provider_event_id, type,
  payload)`; if the row exists, commit and return `200` (replay).
- Resolve the account: `Event.UserID` when the provider carries it,
  else by `provider_customer_id` / `provider_subscription_id`.
- Switch on `Event.Kind`:

| Kind | Effect |
|---|---|
| `checkout_completed` | set `provider_customer_id` (and `provider`) on the account's `subscriptions` row |
| `subscription_started` / `subscription_changed` | upsert plan, interval, status, `current_period_end`, `cancel_at_period_end`, `provider_price_id`; skip if `Event.At` is older than the row's last applied event |
| `subscription_cancelled` | mark canceled; plan falls back to free (`canceled` / `unpaid` / `incomplete_expired` statuses also fall back) |
| `payment_succeeded` | upsert `invoices` (by `provider_invoice_id`) as paid; restore a `past_due` subscription |
| `payment_failed` | upsert invoice as failed; mark subscription `past_due`; queue the "Your payment failed" email |
| `payment_disputed` | suspend the account; record a `chargeback` abuse signal |

- Mirror the effective plan onto `users.plan` and onto every server the
  account owns (what the relay's bandwidth gate reads).
- Mark the ledger row processed; `pg_notify('tier_changed', ...)`;
  commit.

## 3. Test plan

Integration tests drive the handler with an in-process fake `Provider`
whose `ParseWebhook` returns scripted events.

| Test | Pins |
|---|---|
| Provider `none` | every webhook path is 404. |
| Path names a provider other than the configured one | 404. |
| Bad signature | 400; `billing_webhook_forgery` signal. |
| Replay same event id | second delivery 200, no-op. |
| `subscription_started` | row upserted; NOTIFY; plan mirrored onto servers. |
| Out-of-order events | older event skipped. |
| `payment_failed` | invoice failed; `past_due`; email queued. |
| `payment_succeeded` after failure | `past_due` cleared. |
| `payment_disputed` | account suspended; abuse signal. |
| Unknown provider event | 200, no rows. |

## 4. Edge cases — handling table

| Case | Behaviour |
|---|---|
| Out-of-order delivery | Timestamp guard; latest wins. |
| Multiple subs per user | Surface in admin (25.20); manual cleanup. |
| Customer email mismatch | We don't sync. |
| NOTIFY only on commit | Tier cache TTL is the safety net. |
| Manual plan on the account | Webhooks for that account still apply; admin set plan is refused while a provider bills it (25.13). |

## 5. Dependencies

- 25.1, 25.12, 25.13.
- 25.17 (outbox for emails/notifications).
- 25.25 (abuse signals: `billing_webhook_forgery`, `chargeback`).

## 6. Acceptance checklist

- [ ] `/v1/billing/webhook/{provider}` 404 under `none` and for any other provider name.
- [ ] Verification via `Provider.ParseWebhook`; forgery signal on failure.
- [ ] `provider_events` idempotency.
- [ ] Every neutral event kind in §2 handled.
- [ ] NOTIFY `tier_changed` after commit.
- [ ] Tests in §3 pass.
