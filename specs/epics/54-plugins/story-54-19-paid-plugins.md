# Story 54.19 — Paid plugins

> Epic 54 · Phase 3 · Depends on: 54.15, 54.18, the Phase 3 payment provider decision · Needed by: —

## Why

Developers maintain what pays (Plex's plugins faded partly because nobody was paid to keep
them working). The owner asked for free and paid plugins. Maktaba Cloud already issues signed
licences that homes check offline; paid plugins reuse that for entitlements.

Owner decisions of 2026-11-03 that shape this story:

- **No Stripe, ever**: no Stripe, Stripe Connect, Stripe Tax or Stripe-owned services (Lemon
  Squeezy among them).
- **Split:** the developer gets 85 %, Maktaba keeps 15 % of the price.
- **Developer fee:** free to publish free plugins; a one-time $25 to sell.
- **Seller of record and payment processor: decided at Phase 3.** This story is written
  against a provider-agnostic `PaymentProvider` interface so the choice does not change the
  rest of the design.

## Description

### 1. The Phase 3 decision this story waits for

The store resells third-party products, which many sellers of record restrict (Paddle among
them), so a candidate is chosen only after it confirms in writing that it allows plugin
resale. The candidates:

| # | Candidate | Who sells | Who files VAT and sales tax | How developers are paid |
|---|---|---|---|---|
| A | **Paddle** as seller of record | Paddle | Paddle | Paddle pays Maktaba; Maktaba pays each developer their 85 % monthly |
| B | **Maktaba as seller** with a marketplace payment provider such as **Adyen for Platforms** | Maktaba | Maktaba | the provider splits each payment: 85 % to the developer's sub-account, 15 % to Maktaba |
| C | **Developers sell directly** on their own checkout | the developer | the developer | directly; Maktaba takes its 15 % as an invoice to the developer, and verifies the developer's licence keys offline |

Until the owner picks one, everything in §3–§6 is built against fakes and stays off.

### 2. The `PaymentProvider` interface

`cloud/internal/store/payments/provider.go`:

| Method | What it does |
|---|---|
| `OnboardDeveloper(dev) → URL` | starts the provider's identity and payout onboarding (A: Maktaba's own payout details form; B: the provider's sub-account onboarding; C: registering the developer's licence-key public key) |
| `PayoutStatus(dev) → active / pending / restricted` | whether the developer can sell |
| `Checkout(buyer, home, product) → URL` | a hosted checkout for the price, tax included where the provider is seller (A, B); C answers the developer's own checkout URL |
| `ParseEvent(request) → Event` | a verified webhook: paid, refunded, disputed, payout failed |
| `Refund(purchase)` | full refund, reversing the developer's share |
| `Payouts(period) → []Payout` | for A, Maktaba's monthly payout run (85 % of the month's net sales per developer, after refunds); for B and C, a report only |

Implementations: `fake` (tests and staging), then exactly one real provider after the Phase 3
decision. Stripe is never an implementation (CI check T07 fails if a Stripe SDK, API host or
key format appears under `cloud/internal/store/`).

### 3. Developers get paid

- The developer pays the **one-time $25 seller fee** once per developer account, through the
  same `Checkout` (Maktaba is the seller of that fee in every candidate). Publishing free
  plugins needs no fee.
- In the console, **Set up payouts** calls `OnboardDeveloper`. The store keeps only the
  provider's account reference and `PayoutStatus`.
- A developer can price a plugin only when the seller fee is paid **and** payouts are active.

### 4. Products and prices

- `store_products (plugin_id, kind ("one_time" | "yearly"), price per currency, active)`.
  Prices in USD, EUR, GBP, SAR, AED, EGP where the chosen provider supports them, set by the
  developer within store limits ($1–$200 or equivalent).
- A price change applies to new purchases; yearly renewals keep the old price for the current
  term.
- Free plugins stay free: a listed free plugin cannot become paid (a new plugin id is needed),
  so owners are not surprised.

### 5. Buying

1. The admin clicks **Buy** in the home's Browse (or on maktaba.tv). The home opens the
   maktaba.tv checkout page for the signed-in Maktaba account and the home id (linked homes;
   unlinked homes buy on maktaba.tv and enter the home's claim code).
2. maktaba.tv calls `Checkout`. With candidates A and B the buyer pays on the provider's hosted
   page; with C the buyer is sent to the developer's checkout with the home id attached.
3. The provider's webhook (`cloud/internal/handlers/store/payments_webhook.go`, verified by
   `ParseEvent`) records `store_purchases (account, home, plugin, product, amount,
   maktaba_share, developer_share, provider_ref, status, period_end)`. With C, the developer's
   server tells the store about a sale through a signed `POST /store/v1/sales` (developer key),
   and Maktaba's 15 % is invoiced monthly.
4. The home downloads the package and the **entitlement**.

**Split:** `maktaba_share = 15 %` of the price, `developer_share = 85 %`, computed by one
function with a shared test table (rounding: Maktaba's share is rounded down to the cent, the
developer gets the rest). Nothing else is taken from the developer's share by the store.

### 6. Entitlements

- The cloud's licence issuer (`cloud/internal/license/issuer.go`) signs a **plugin entitlement**
  `{home_id, plugin_id, product, valid_from, valid_until (purchase: +31 days rolling; yearly:
  period end), refreshed_at}` with the existing licence key; homes verify with the existing
  licence verifier.
- With candidate C, the entitlement is the developer's own licence key for the home, signed by
  the developer's registered licence-key public key and checked offline the same way; the
  store countersigns the public key, not each licence.
- The home refreshes entitlements daily with its licence refresh (the same grace rules: 31 days
  plus `SkewGrace`). A one-time purchase renews forever while the purchase is not refunded.
- **No valid entitlement** → the plugin is *Waiting* `licence_missing` (Epic 51 §4.10): held,
  queue kept, data kept. It never deletes data and never blocks other plugins.
- Packages of paid plugins are downloaded with a short-lived signed URL issued against a valid
  entitlement. A copied package installed on another home installs but stays *Waiting*
  `licence_missing`.
- Moving to a new home: the buyer's account can move a purchase to another home of theirs
  (twice a year), which revokes the old home's entitlement at its next refresh.

### 7. Refunds and disputes

- Refund within 14 days on request in the account page, automatic through `Refund` (the
  developer's share is reversed, or for A deducted from the next monthly payout); the
  entitlement ends at the next refresh.
- A lost dispute ends the entitlement and reverses the developer's share the same way.
- A plugin `disable`-revoked for malware: buyers are refunded automatically; the developer's
  share is reversed.

### 8. Plugins never touch money

No capability, API call, panel or string can show a price, a "buy" link or a paywall. The
listing is the only place prices appear (review guideline, and the no-URL rules already stop
links).

## Acceptance criteria

- **Given** a developer who has not paid the $25 seller fee, or whose payouts are not active,
  **when** they set a price,
  **then** `403 seller_fee_required` or `403 payouts_required`.
- **Given** a developer publishing a free plugin,
  **when** they submit it,
  **then** no fee is asked.
- **Given** a $10 purchase confirmed by the fake provider,
  **when** the webhook is processed,
  **then** the purchase row records Maktaba $1.50 and the developer $8.50, and the home gets a
  valid entitlement.
- **Given** an entitlement that lapsed past grace,
  **when** the home's runner passes,
  **then** the plugin is *Waiting* `licence_missing`, its queue and data kept; a new
  entitlement brings it back with nobody touching it.
- **Given** a refund within 14 days,
  **when** processed,
  **then** the developer's share is reversed and the entitlement ends at the next refresh.
- **Given** a paid plugin's package copied to another home,
  **when** installed there,
  **then** it stays *Waiting* `licence_missing`.
- **Given** any change under `cloud/internal/store/`,
  **when** CI runs,
  **then** the no-Stripe check finds no Stripe SDK import, API host (`api.stripe.com`) or key
  prefix (`sk_live_`, `sk_test_`, `pk_live_`, `pk_test_`, `whsec_`) in the payment code.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (go, cloud) | split function, shared table | 85/15 on prices and currencies | Maktaba rounded down, developer the rest, sums equal |
| T02 | integration (PG, cloud) | fake provider events: paid, refunded, disputed, payout failed | process | purchase rows and reversals |
| T03 | unit (go) | entitlement sign/verify with the licence key; candidate C developer key | verify | valid / expired / other home |
| T04 | integration (PG, go home) | lapse past grace | runner | Waiting `licence_missing`; back on renew |
| T05 | role tests (cloud) | seller fee, pricing, purchase, refund, move, sales routes | roles | developer of other plugin, buyer, other user refused |
| T06 | unit | strings en + ar | checkout, account and console pages | complete |
| T07 | CI check (`tools/ci/no_stripe.sh`) | `cloud/internal/store/` tree | grep | no Stripe SDK, host or key prefix in store payment code |
| T08 | integration (cloud) | candidate A monthly payout run on the fake | `Payouts(month)` | 85 % of net sales per developer after refunds |

All payment tests use the fake provider and recorded fixtures; no live payment calls
(standing rule).

## Edge cases

- **A buyer's country the chosen provider can't sell to**: Buy is disabled with an explanation;
  free plugins are unaffected.
- **A developer's payout account restricted** by the provider: new sales paused; existing
  entitlements continue.
- **Price 0 promotional**: not supported; promotion codes only if the chosen provider has them.
- **The provider is changed later**: existing entitlements are the cloud's own signatures and
  keep working; only new purchases use the new provider.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/internal/store/payments/provider.go`, `fake.go` (new) | §2 interface and the fake. |
| `cloud/internal/store/payments/<provider>.go` (new, after the Phase 3 decision) | The one real implementation. |
| `cloud/internal/store/products.go`, `purchases.go`, `split.go`, `payouts.go` (new) | §3–§7. |
| `cloud/internal/handlers/store/payments_webhook.go` (new) | Provider events. |
| `cloud/internal/license/issuer.go` | Plugin entitlement claims. |
| `api/internal/plugins/entitlements.go` (new) | Home refresh and checks; *Waiting* `licence_missing`. |
| `cloud/migrations/NNNN_store_payments.sql` | Seller fees, products, purchases, payouts. |
| `tools/ci/no_stripe.sh` (new) | T07. |
| Cloud web app | Seller fee, checkout, account purchases, payouts in the console; en + ar. |

### Tests

- Unit T01, T03, T06; integration T02, T04, T08; role T05; CI T07. `store` job.

### Rollout

Off until the owner picks the payment provider and seller of record at Phase 3 (after written
confirmation that it allows resale of third-party plugins) and sets up the live account (an
owner action; nothing in this epic calls a live payment API).

### Rollback

Pause paid listings (store admin switch). Existing entitlements keep refreshing; nothing is
deleted.

### Dependencies

54.15, 54.18; Epic 16 licences; the Phase 3 payment provider decision (README §14 item 2).
