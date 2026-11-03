# Story 25.13 — Checkout

> Epic 25 · Cloud relay · Phase 3 (billing)

## Description

Users upgrade by clicking a button that takes them to the payment
provider's hosted checkout. The payment provider is TBD:
the cloud talks to it only through the `billing.Provider` interface
(`cloud/internal/billing/provider.go`), which can open a hosted
checkout, open the provider's customer portal, and verify a webhook
(25.14) and translate it into provider-neutral events.

`[billing] provider` (env `MAKTABA_CLOUD_BILLING_PROVIDER`) picks the
provider; config validation rejects any provider the cloud does not
ship. The only provider shipped is `none`, the default. With `none`:

- `GET /v1/billing/plans` reports `payments_enabled: false` and no
  plan is `purchasable`; the web app shows paid plans with a disabled
  "Payments coming soon" button.
- `POST /v1/billing/checkout` and `POST /v1/billing/portal` answer
  `503 payments_not_set_up`.
- A service admin sets a user's plan by hand with
  `POST /v1/admin/accounts/{id}/plan` (`{plan, reason}`, reason
  required). The change is audited as `admin.account.set_plan`, the
  subscription row carries provider `manual`, and it is not counted
  as revenue.

Once a real provider ships, the cloud asks it for a checkout session
and returns the session URL; the client redirects (web) or opens the
URL in a system browser (mobile/desktop). On success the provider
redirects back to `https://app.maktaba.tv/billing/success`; on cancel,
to `/billing/canceled`. The webhook handler (25.14) is the authority on
what actually happened.

Endpoints:

- `POST /v1/billing/checkout`
  Body: `{plan: "pro"|"family"}`.
  Response: `{checkout_url, session_id}`.
- `POST /v1/billing/portal`
  Returns `{portal_url}`, the provider's customer portal, where users
  self-serve cancel, change plan, update payment method, and see
  invoices. We do not reimplement these flows.

Behavior details:

- **Account link.** The checkout carries our account id
  (`CheckoutParams.UserID`), so every later event names the account
  even before the provider's customer id is known. The provider's
  customer id is persisted as `provider_customer_id` on the user's
  `subscriptions` row (single source of truth; never denormalized onto
  `users`).
- **Tax, promo codes, currency.** Left to the provider's hosted
  checkout; we do not handle tax ourselves.
- **Apple/Google IAP.** Out of scope. iOS app shows the upgrade button
  only when the device is *not* iOS (App Store rules).
- **Plan change while active.** Goes to the customer portal (the
  provider handles proration). The cloud learns about it from a
  `subscription_changed` webhook event (25.14).

## Acceptance criteria

- **Given** the provider is `none`,
  **when** a user calls `POST /v1/billing/checkout` or
  `POST /v1/billing/portal`,
  **then** the response is `503 payments_not_set_up`.
- **Given** the provider is `none`,
  **when** the web app renders the plan comparison,
  **then** paid plans show a disabled "Payments coming soon" button.
- **Given** a service admin,
  **when** they `POST /v1/admin/accounts/{id}/plan` with a plan and a
  reason,
  **then** the response is `204`, the account and its servers move to
  that plan, the subscription's provider is `manual`, an
  `admin.account.set_plan` audit row is written, and revenue is
  unchanged.
- **Given** the same call without a reason, or from a non-admin,
  **then** it is rejected (`422` / `403`).
- **Given** a provider that takes payments and an authenticated user
  with no subscription,
  **when** they `POST /v1/billing/checkout` with `{plan: "pro"}`,
  **then** the response is `200 {checkout_url, session_id}`.
- **Given** the user already has a subscription,
  **when** they request checkout,
  **then** the response is `409 already_subscribed` (use the portal).
- **Given** an unknown or free plan,
  **then** the response is `422 bad_plan`.
- **Given** a user with no provider customer,
  **when** they `POST /v1/billing/portal`,
  **then** the response is `409 no_customer`.
- **Given** an unauthenticated request,
  **then** the response is `401`.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | integration | provider `none` | checkout / portal | 503 `payments_not_set_up` |
| T02 | integration | provider `none` | `GET /v1/billing/plans` | `payments_enabled=false`, nothing purchasable |
| T03 | unit        | config `provider = "acme"` | validate | rejected: unknown provider |
| T04 | integration | admin sets plan with reason | call | 204; provider `manual`; audit row; no revenue |
| T05 | integration | admin sets plan without reason | call | 422 |
| T06 | integration | non-admin sets plan | call | 403 |
| T07 | integration | fake provider, new user | checkout | 200 with URL |
| T08 | integration | fake provider, subscribed user | checkout | 409 `already_subscribed` |
| T09 | integration | fake provider, no customer | portal | 409 `no_customer` |
| T10 | unit        | invalid plan name | checkout | 422 `bad_plan` |

## Edge cases

- **iOS app store enforcement.** v1 omits IAP. The web surface is the
  only payment path on iOS.
- **Manual plan while a provider bills.** Setting a plan by hand is
  refused with `409 provider_managed` when a payment provider is
  billing that account.
- **Failed payment after success.** The provider sends
  `payment_failed`; the webhook (25.14) marks the subscription
  `past_due`. We do not refund automatically.
- **`return_url` validation.** Success and cancel URLs come from
  config, never from the request body, which defends against
  open-redirect.

## Files / packages

- `cloud/internal/billing/provider.go` — `Provider` interface, `None`.
- `cloud/internal/billing/plans.go` — plan constants.
- `cloud/internal/handlers/billing/billing.go` — checkout, portal.
- `cloud/internal/handlers/admin/admin.go` — `SetPlan`.
- `cloud/configs/cloud.example.toml` — `[billing] provider = "none"`.

## Open questions

- **Which payment provider.** TBD.
- **In-app purchase plan.** When (if ever) do we add Apple/Google IAP?
  Defer to a dedicated epic if growth demands it.
