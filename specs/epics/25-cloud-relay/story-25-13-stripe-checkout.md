# Story 25.13 — Stripe checkout session

> Epic 25 · Cloud relay · Phase 3 (billing)

## Description

Users upgrade by clicking a button that takes them to Stripe-hosted
checkout. The cloud creates a Stripe Checkout Session and returns the
session URL; the client redirects (web) or opens the URL in a system
browser (mobile/desktop). On success, Stripe redirects back to
`https://app.maktaba.app/billing/success?session_id={CHECKOUT_SESSION_ID}`;
on cancel, to `/billing/canceled`. The webhook handler (25.14) is the
authority on what actually happened.

Endpoints:

- `POST /api/billing/checkout`
  Body: `{tier: "pro"|"family", interval: "monthly"|"yearly",
  promo_code: <optional>, return_url: <optional>}`.
  Response: `{url: "https://checkout.stripe.com/c/pay/cs_..."}`.
- `POST /api/billing/portal`
  Returns a Stripe Customer Portal URL where users self-serve
  cancel, change plan, update payment method, see invoices.
  We do not reimplement these flows.
- `POST /api/billing/upgrade-preview`
  Returns proration calculation (Stripe `upcoming` invoice).

Behavior details:

- **Idempotency.** Each call to `/checkout` sets
  `Idempotency-Key: <sha256(user_id + tier + interval + day-of-week)>`
  so rapid double-clicks return the same session URL.
- **Customer creation.** First-time payers get a Stripe customer
  created with `email` and `metadata: {maktaba_user_id: <uuid>}`.
  We persist `stripe_customer_id` on the user's `subscriptions`
  row (single source of truth; never denormalized onto `users`).
- **Tax.** Stripe Tax is enabled; addresses collected in
  Checkout. We do not handle tax ourselves.
- **Apple/Google IAP.** Out of scope. iOS app shows the
  upgrade button only when the device is *not* iOS — App Store
  rules. (Apple's 2024 ruling allows linking out for "reader"
  apps; safest to suppress in v1 and wire up later. Document.)
- **Promo codes.** Stripe coupons via promotion codes; we accept
  the user-typed code and pass it to Checkout. If invalid,
  Stripe surfaces it.
- **Currency.** USD primary; EUR/GBP at parity for v1; tax
  handled by Stripe.
- **Plan change while active.** Goes to Customer Portal
  (Stripe handles proration). The cloud receives
  `customer.subscription.updated` from the webhook (25.14).

## Acceptance criteria

- **Given** an authenticated user with no subscription,
  **when** they `POST /api/billing/checkout` with
  `{tier: "pro", interval: "monthly"}`,
  **then** the response is `200 {url: "https://checkout.stripe.com/..."}`
  and a Stripe customer is created (or reused).
- **Given** the user has a Stripe customer,
  **when** they `POST /api/billing/portal`,
  **then** the response is a portal URL valid for 5 minutes.
- **Given** an unauthenticated request,
  **when** the call hits `/api/billing/checkout`,
  **then** the response is `401`.
- **Given** the user is already on Pro,
  **when** they request checkout for Pro,
  **then** the response is `409 already_on_plan` with a
  link to the portal.
- **Given** Stripe is unavailable,
  **when** the call to checkout fails,
  **then** the response is `502 stripe_unavailable` with
  `Retry-After: 30`.
- **Given** a stale checkout-session URL (24h+),
  **when** the user clicks it,
  **then** Stripe expires it; the user lands on
  `/billing/canceled?reason=session_expired`.
- **Given** the user lands on `/billing/success?session_id=`,
  **when** the page loads,
  **then** the page polls
  `GET /api/billing/subscription` until status is
  `active` (the webhook (25.14) fired) or 30s elapse.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | mock Stripe `checkout.sessions.create` | call | response includes URL |
| T02 | unit        | invalid plan name | call | 400 `unknown_plan` |
| T03 | integration | rapid double-click | two POSTs in 1s | identical URL (idempotency key) |
| T04 | integration | new customer | call | Stripe customer created with `maktaba_user_id` metadata |
| T05 | integration | existing customer | call | reused; no second customer |
| T06 | integration | promo code valid | call | session has discount applied |
| T07 | integration | promo code invalid | call | session created; Stripe surfaces error to user at payment time |
| T08 | regression  | network drop mid-Stripe call | call | retries up to 3 times; surfaces 502 if all fail |
| T09 | unit        | success URL contains `{CHECKOUT_SESSION_ID}` placeholder | render | replaced by Stripe |
| T10 | regression  | iOS user agent | call | `/api/billing/checkout` returns 451 `apple_iap_required` (v1 surface) |

## Edge cases

- **iOS app store enforcement.** v1 omits IAP. The web
  surface is the only payment path on iOS. Document at
  install time.
- **Mid-flow currency change.** Not supported; Stripe
  resolves currency at session creation.
- **Customer with no email.** Apple private-relay path:
  the Stripe customer's `email` is the relay address; tax
  receipts go through it.
- **Failed payment after success.** Stripe issues
  `invoice.payment_failed`; webhook (25.14) downgrades.
  We do not refund automatically.
- **Stripe rate limits.** 100 read/s, 100 write/s default.
  We never approach these in v1; documented as a v2
  scaling concern.
- **`return_url` validation.** Allowed list:
  `https://app.maktaba.app/*`, `https://web.maktaba.app/*`,
  custom-scheme `maktaba://*`. Defends against open-redirect.
- **B2B / VAT.** Stripe collects VAT IDs at checkout; v1
  doesn't issue formal VAT invoices ourselves — Stripe's
  PDFs suffice.
- **Failed customer creation race.** If two checkouts
  arrive in parallel for a customer-less user, only one
  creates the Stripe customer (`SELECT … FOR UPDATE` on
  `cloud_users` row); the other reuses.
- **Partial-region availability.** If Stripe doesn't
  process payments in a country, surface
  `region_unavailable` from the API.

## Files / packages

- `cloud/internal/billing/checkout.go`.
- `cloud/internal/billing/portal.go`.
- `cloud/internal/billing/customer.go` — find-or-create.
- `cloud/internal/billing/plans.go` — plan ID constants
  (`price_xxxxx` from Stripe dashboard).
- `cloud/configs/cloud.example.toml` —
  `[stripe] secret_key=ENV, webhook_secret=ENV,
  price_pro_monthly=price_..., price_pro_yearly=price_...,
  price_family_monthly=price_..., price_family_yearly=price_...`.

## Open questions

- **In-app purchase plan.** When (if ever) do we add
  Apple/Google IAP? Defer to a dedicated Epic 26 if
  growth demands it.
- **Crypto / SEPA.** Stripe supports both; v1 cards only.
