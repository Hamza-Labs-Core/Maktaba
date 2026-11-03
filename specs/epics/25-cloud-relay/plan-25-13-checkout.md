# Implementation Plan — Story 25.13 Checkout

> Companion to [story-25-13-checkout.md](story-25-13-checkout.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Provider seam | `billing.Provider` (`cloud/internal/billing/provider.go`): `CreateCheckout`, `CreatePortal`, `ParseWebhook`, plus `Name`, `Enabled`, `Sells(plan)`. Handlers never call a provider API directly. The payment provider is TBD. |
| Provider choice | `[billing] provider` (env `MAKTABA_CLOUD_BILLING_PROVIDER`), default `none`. `billing.New(name)` returns the provider; config validation rejects any name the cloud does not ship. |
| Endpoints | `GET /v1/billing/plans`, `GET /v1/billing/me`, `POST /v1/billing/checkout`, `POST /v1/billing/portal`. Webhook (25.14) is separate. Admin: `POST /v1/admin/accounts/{id}/plan`. |
| Provider customers | The provider's customer id lives on the `subscriptions` table only (`provider_customer_id`, single source of truth); we **do not** denormalize it onto `users`. It is linked by the `checkout_completed` webhook event; the checkout carries our account id so every event names the account. |
| Tier vocabulary | Canonical: `plan IN ('free','pro','family')` and `interval IN ('monthly','yearly')`. Matches `architecture.md` §13.10. |
| Apple IAP | Out of scope in v1; the iOS app hides the upgrade button. |
| Out of scope | Webhook processing (25.14). Family invites flow UI (deferred). |

## 1. Schema

Migration `00060001_billing.sql` (slot 0006) creates `subscriptions`,
`invoices`, `family_members` and the webhook event ledger. Migration
`00160001_billing_provider_neutral.sql` makes them provider-neutral:

- `subscriptions`: `provider_customer_id` (unique),
  `provider_subscription_id` (unique), `provider_price_id`, and a
  nullable `provider` column (NULL = no provider has touched the row;
  `manual` = set by a service admin).
- `invoices`: keyed by `provider_invoice_id`, with a `provider` column.
- `provider_events`: the webhook idempotency ledger, primary key
  `(provider, provider_event_id)`, with the event type, payload and
  processed time.

(25.14 adds nothing; it consumes these tables.)

## 2. The `none` provider

`billing.None` is the only provider shipped:

- `Enabled()` and `Sells(plan)` are false, so `GET /v1/billing/plans`
  reports `payments_enabled: false` and no plan is `purchasable`. The
  web app renders paid plans with a disabled "Payments coming soon"
  button (English and Arabic).
- `CreateCheckout` and `CreatePortal` return `billing.ErrNotConfigured`;
  the handlers map it to `503 payments_not_set_up`.
- `ParseWebhook` returns `ErrNotConfigured`; the webhook route answers
  `404` (25.14).

## 3. Manual plan set

`POST /v1/admin/accounts/{id}/plan`, body `{plan, reason}`, service
admins only (enforced server-side, with a role test):

- `reason` is required (`422` without it); unknown plan → `422 bad_plan`.
- Refused with `409 provider_managed` when a payment provider is billing
  the account.
- Upserts the subscription with `provider = 'manual'`, mirrors the plan
  onto `users.plan` and every server the account owns, NOTIFYs
  `tier_changed`, and writes an `admin.account.set_plan` audit row.
- Manual plans are excluded from revenue (25.21).

## 4. Checkout and portal (a provider that takes payments)

- `POST /v1/billing/checkout` `{plan}`: `422 bad_plan` for an unknown or
  free plan, `409 already_subscribed` when the account already has a
  paid subscription (use the portal). Otherwise
  `Provider.CreateCheckout` with the plan, our account id, the
  returning payer's customer id or their email, and the success/cancel
  URLs from config. Returns `{checkout_url, session_id}`.
- `POST /v1/billing/portal`: `409 no_customer` without a
  `provider_customer_id`; otherwise `Provider.CreatePortal`, returning
  `{portal_url}`.
- `GET /v1/billing/me`: `{plan, status, interval?, current_period_end?,
  cancel_at_period_end, has_customer}`.

Success and cancel URLs come from config, never the request body, so
there is no open-redirect surface.

## 5. Test plan

| Test | Pins |
|---|---|
| Provider `none`: checkout and portal | 503 `payments_not_set_up`. |
| Provider `none`: plans | `payments_enabled=false`; nothing purchasable. |
| Config with an unknown provider | Validation error. |
| Admin set plan | 204; provider `manual`; plan mirrored; audit row; no revenue. |
| Admin set plan without reason / as non-admin | 422 / 403. |
| Admin set plan on a provider-billed account | 409 `provider_managed`. |
| Fake provider: checkout | 200 with URL; account id passed to the provider. |
| Fake provider: already subscribed | 409 `already_subscribed`. |
| Fake provider: portal without customer | 409 `no_customer`. |
| Unknown plan | 422 `bad_plan`. |

The integration tests use an in-process fake `Provider`, so they need no
network and no real provider.

## 6. Dependencies

- 25.1, 25.2 (`users` table).
- 25.12 (consumes `subscriptions`).
- 25.14 (the webhook updates the same `subscriptions` row in place).
- 25.20 / 25.21 (admin console's Set plan, revenue excludes `manual`).

## 7. Acceptance checklist

- [ ] `billing.Provider` interface and `none` provider.
- [ ] `[billing] provider` config, env override, validation.
- [ ] Checkout / portal answer 503 `payments_not_set_up` under `none`.
- [ ] Web shows "Payments coming soon" under `none`.
- [ ] Manual admin plan set, audited, not revenue.
- [ ] Tests in §5 pass.
