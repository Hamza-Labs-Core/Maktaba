# Story 54.19 — Paid plugins

> Epic 54 · Phase 3 · Depends on: 54.15, 54.18, the Phase 3 payment provider decision · Needed by: —

## Why

Developers maintain what pays (Plex's plugins faded partly because nobody was paid to keep
them working). Both free and paid plugins are required. The store sells them **on its own**:
its own payment provider account and seller-of-record choice, its own payouts, tax records and
refunds, and its own signed entitlements that homes check offline. Nothing here uses Maktaba
Cloud's billing (Epic 50.19), its licence issuer or its accounts, and no home calls the cloud to
use a paid plugin.

Decisions of 2026-11-03 that shape this story:

- **The store is independent of Maktaba Cloud**, payments included (54.15 "Why independent").
- **No Stripe, ever**: no Stripe, Stripe Connect, Stripe Tax or Stripe-owned services (Lemon
  Squeezy among them), and no provider that runs on Stripe underneath.
- **Split:** the developer gets 85 %, Maktaba keeps 15 % of the price.
- **Developer fee:** free to publish free plugins; a one-time $25 to sell.
- **Seller of record and payment processor: an owner decision at Phase 3.** This story is
  written against a provider-agnostic `PaymentProvider` interface so the choice does not change
  the rest of the design.

## Description

### 1. The Phase 3 decision this story waits for

The store resells third-party products, which many sellers of record restrict (Paddle among
them), so a candidate is chosen only after it confirms in writing that it allows plugin resale
and that it does not process through Stripe. The provider account is the store's own, opened
for the store and separate from any account used for cloud billing (separate merchant account,
separate API keys, separate webhook secrets, separate payout bank details).

| # | Model | Who sells | Who files VAT and sales tax | How developers are paid | Non-Stripe candidates to evaluate |
|---|---|---|---|---|---|
| A | **A merchant of record sells for the store** | the provider | the provider | the provider pays the store; the store pays each developer their 85 % monthly | Paddle, FastSpring, 2Checkout (Verifone) |
| B | **The store is the seller** with a marketplace payment provider | the store's legal entity | the store | the provider splits each payment: 85 % to the developer's sub-account, 15 % to the store | Adyen for Platforms, PayPal (marketplace / Commerce Platform), Checkout.com; for the Gulf and Egypt, Tap Payments, PayTabs or Paymob |
| C | **Developers sell directly** on their own checkout | the developer | the developer | directly; the store invoices its 15 % monthly, and verifies the developer's licence keys offline | none needed for sales; the store's 15 % invoices and the $25 fee still need one of A or B |

Until the owner picks one, everything in §3–§7 is built against fakes and stays off.

### 2. The `PaymentProvider` interface

`store/payments/provider.ts`:

| Method | What it does |
|---|---|
| `onboardDeveloper(dev) → URL` | starts the provider's identity and payout onboarding (A: the store's own payout details form; B: the provider's sub-account onboarding; C: registering the developer's licence-key public key) |
| `payoutStatus(dev) → active / pending / restricted` | whether the developer can sell |
| `checkout(buyer, home, product) → URL` | a hosted checkout for the price, tax included where the provider or the store is seller (A, B); C answers the developer's own checkout URL |
| `parseEvent(request) → Event` | a verified webhook: paid, refunded, disputed, payout failed |
| `refund(purchase)` | full refund, reversing the developer's share |
| `payouts(period) → Payout[]` | for A, the store's monthly payout run (85 % of the month's net sales per developer, after refunds); for B and C, a report only |

Implementations: `fake` (tests and staging), then exactly one real provider after the Phase 3
decision. Stripe is never an implementation (CI check T07 fails if a Stripe SDK, API host or key
format appears under `store/`).

Webhooks go to the `store-web` Worker at `POST plugins.maktaba.tv/v1/payments/webhook/<provider>`:
verified by `parseEvent` with the provider's signing secret (a Worker secret), idempotent on
the provider's event id (`provider_events` unique key in D1 `store-commerce`), and answered
`200` only after the D1 write commits, so the provider retries anything lost. Provider
secrets are set by the owner as Worker secrets, never in files or CI logs.

### 3. Developers get paid

- The developer pays the **one-time $25 seller fee** once per developer account, through the
  same `checkout` (the store is the seller of that fee in every model). Publishing free plugins
  needs no fee.
- In the console, **Set up payouts** / «إعداد المدفوعات» calls `onboardDeveloper` (re-auth
  applies, 54.15 §4). The store keeps only the provider's account reference and
  `payoutStatus`; bank details stay with the provider (A's form posts to the provider's hosted
  page where it has one).
- A developer can price a plugin only when the seller fee is paid **and** payouts are active.
- **Payouts (A):** a monthly Cron Trigger runs `payouts(month)`, writes `payouts` rows and a
  signed statement per developer to R2 (`statements/<developer>/<month>.pdf`, en + ar), and
  sends the provider's payout batch, which a `store_admin` approves with re-auth before it is
  sent. Payouts under $25 roll over to the next month.

### 4. Products and prices

- `products (plugin_id, kind ("one_time" | "yearly"), price per currency, active)` in
  `store-commerce`. Prices in USD, EUR, GBP, SAR, AED, EGP where the chosen provider supports
  them, set by the developer within store limits ($1–$200 or equivalent).
- A price change applies to new purchases; yearly renewals keep the old price for the current
  term.
- Free plugins stay free: a listed free plugin cannot become paid (a new plugin id is needed),
  so owners are not surprised.

### 5. Buying

1. The home must be **linked to a store account** (54.15 §5). An unlinked home shows **Link
   this home to buy** / «اربط هذا الجهاز الرئيسي للشراء» instead of Buy.
2. The admin clicks **Buy** / «اشترِ» in the home's Browse (or on `plugins.maktaba.tv`, then
   picks one of the account's linked homes). The home opens
   `https://plugins.maktaba.tv/buy/<plugin id>` in the admin's browser with a one-time,
   home-signed purchase intent (posted, not in the URL); the buyer signs in to the store
   account linked to that home.
3. The store calls `checkout`. With A and B the buyer pays on the provider's hosted page; with
   C the buyer is sent to the developer's checkout with the store's home id attached.
4. The provider's webhook records `purchases (account, home, plugin, product, amount, currency,
   maktaba_share, developer_share, provider_ref, status, period_end)`. With C, the developer's
   server reports a sale through a signed `POST /v1/sales` (developer key), and the store's 15 %
   is invoiced monthly.
5. The home downloads the **entitlement** (`GET /v1/homes/{id}/entitlements`, signed by the
   home): after a Buy it started it checks every 15 s for up to 15 minutes, otherwise daily.
   Then it downloads the package.

**Split:** `maktaba_share = 15 %` of the price, `developer_share = 85 %`, computed by one
function with a shared test table (rounding: Maktaba's share is rounded down to the cent, the
developer gets the rest). Nothing else is taken from the developer's share by the store; the
provider's own fees are shown to developers as the provider states them.

### 6. Entitlements: signed by the store, checked offline

- The store signs a **plugin entitlement** with its entitlement key E (54.14, `use:
  "entitlement"`, listed in `store-keys.json` under the embedded plugin root):
  `{v: 1, home_id (the store's), home_key_fp (sha256 of the home store key), plugin_id,
  product, purchase_id, valid_from, valid_until (one-time: +31 days rolling; yearly: period end,
  at most +31 days ahead and rolled), issued_at}`.
- The home verifies it **offline**: embedded `plugins-root.json` → `store-keys.json` → E →
  the entitlement, and that `home_id` and `home_key_fp` are its own. No call to Maktaba Cloud,
  its licence issuer or its licence verifier; the grace rules (31 days plus `SkewGrace`) are
  the same numbers as Epic 16's, in the home's own `api/internal/plugins/entitlements.go`.
- The home refreshes entitlements daily with a signed request to the store. A store outage
  shorter than 31 days changes nothing for a paying home; a cloud outage of any length changes
  nothing at all.
- With model C, the entitlement is the developer's own licence for the home, signed by the
  developer's registered licence-key public key and checked offline the same way; the store
  countersigns the developer's public key, not each licence.
- A one-time purchase renews forever while the purchase is not refunded.
- **No valid entitlement** → the plugin is *Waiting* `licence_missing` (Epic 51 §4.10): held,
  queue kept, data kept. It never deletes data and never blocks other plugins. Words: "Waiting
  for this plugin's licence" / «في انتظار ترخيص هذه الإضافة».
- **Packages of paid plugins** are downloaded from `plugins-cdn.maktaba.tv/paid/<sha256>` with
  a signed URL (`t`: home id, sha256, expiry 10 minutes, signed by the store) issued only
  against a valid entitlement; the `store-cdn` Worker checks it and streams from the private
  R2 prefix. A copied package installed on another home installs but stays *Waiting*
  `licence_missing`.
- **Moving to a new home:** the buyer's store account can move a purchase to another of its
  linked homes (twice a year, re-auth), which ends the old home's entitlement at its next
  refresh.

### 7. Refunds, disputes and tax: owned by the store

- **Refunds** within 14 days on request in the store account page, automatic through `refund`
  (the developer's share is reversed, or for A deducted from the next monthly payout); the
  entitlement ends at the next refresh. Later refunds are a `store_admin` decision.
- **Disputes:** a lost dispute ends the entitlement and reverses the developer's share the
  same way.
- A plugin `disable`-revoked for malware: buyers are refunded automatically; the developer's
  share is reversed.
- **Tax:** with A the merchant of record files; with B the store's entity registers and
  files, using the provider's tax calculation; with C the developer does. In every model the
  store keeps its own records (purchases, refunds, payouts, invoices for its 15 % and the $25
  fee) in D1 and as monthly exports in R2 for 10 years. The store never uses the cloud's
  billing records.

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
- **Given** a home not linked to a store account,
  **when** the admin opens a paid plugin in Browse,
  **then** Buy is replaced by "Link this home to buy" / «اربط هذا الجهاز الرئيسي للشراء».
- **Given** a $10 purchase confirmed by the fake provider,
  **when** the webhook is processed,
  **then** the purchase row records Maktaba $1.50 and the developer $8.50, and the home gets a
  valid entitlement signed by E.
- **Given** the same webhook delivered twice,
  **when** processed,
  **then** one purchase row exists.
- **Given** a home with a valid entitlement and Maktaba Cloud unreachable (and, separately, the
  store unreachable for 10 days),
  **when** the runner passes,
  **then** the paid plugin keeps running and no request goes to any cloud host.
- **Given** an entitlement that lapsed past grace,
  **when** the home's runner passes,
  **then** the plugin is *Waiting* `licence_missing`, its queue and data kept; a new
  entitlement brings it back with nobody touching it.
- **Given** an entitlement for another home id, or for this home id with another home key,
  **when** verified,
  **then** it is refused.
- **Given** a refund within 14 days,
  **when** processed,
  **then** the developer's share is reversed and the entitlement ends at the next refresh.
- **Given** a paid plugin's package copied to another home,
  **when** installed there,
  **then** it stays *Waiting* `licence_missing`.
- **Given** any change under `store/`,
  **when** CI runs,
  **then** the no-Stripe check finds no Stripe SDK import, API host (`api.stripe.com`,
  `js.stripe.com`) or key prefix (`sk_live_`, `sk_test_`, `pk_live_`, `pk_test_`, `rk_live_`,
  `whsec_`) in the store code.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (ts) | split function, shared table | 85/15 on prices and currencies | Maktaba rounded down, developer the rest, sums equal |
| T02 | integration (ts, Miniflare D1) | fake provider events: paid, refunded, disputed, payout failed, duplicates | process | purchase rows, reversals, idempotent |
| T03 | unit (go home + ts store) | entitlement signed by E (ts) and verified (go); model C developer key | verify | valid / expired / other home / other home key / wrong key use |
| T04 | integration (PG, go home) | lapse past grace; cloud and store hosts blackholed | runner | runs while valid; Waiting `licence_missing` after; back on renew |
| T05 | role tests (ts) | seller fee, pricing, purchase, refund, move, sales, payout approval routes | roles | developer of other plugin, buyer of another account, unlinked home, cloud token refused; payout approval needs `store_admin` + re-auth |
| T06 | unit | strings en + ar | checkout, account, link-to-buy and console pages; `licence_missing` words | complete |
| T07 | CI check (`tools/ci/no_stripe.sh`) | `store/` tree and its lockfiles | grep | no Stripe SDK, host or key prefix |
| T08 | integration (ts) | model A monthly payout run on the fake | `payouts(month)` | 85 % of net sales per developer after refunds; under $25 rolls over |
| T09 | integration (ts) | `/paid/*` with an expired, a forged and another home's `t` | GET | 403 each; valid one streams |

All payment tests use the fake provider and recorded fixtures; no live payment calls
(standing rule).

## Edge cases

- **A buyer's country the chosen provider can't sell to**: Buy is disabled with an explanation;
  free plugins are unaffected.
- **A developer's payout account restricted** by the provider: new sales paused; existing
  entitlements continue.
- **Price 0 promotional**: not supported; promotion codes only if the chosen provider has them.
- **The provider is changed later**: existing entitlements are the store's own signatures and
  keep working; only new purchases use the new provider.
- **The entitlement key E is rotated**: the new key is listed in `store-keys.json` first; homes
  get new entitlements at the next daily refresh; old ones stay valid to their `valid_until`.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `store/payments/provider.ts`, `fake.ts` (new) | §2 interface and the fake. |
| `store/payments/<provider>.ts` (new, after the Phase 3 decision) | The one real implementation. |
| `store/commerce/products.ts`, `purchases.ts`, `split.ts`, `payouts.ts`, `entitlements.ts` (new) | §3–§7. |
| `store/web/payments_webhook.ts` (new) | Provider events. |
| `store/cdn/paid.ts` (new) | Signed URLs for paid packages. |
| `store/migrations/commerce/NNNN_payments.sql` (new) | Seller fees, products, purchases, entitlements, payouts, provider events. |
| `api/internal/plugins/entitlements.go` (new) | Home refresh and offline checks; *Waiting* `licence_missing`. |
| `shared/go/pluginsig/` | Entitlement verification with `use: "entitlement"` keys. |
| `tools/ci/no_stripe.sh` (new) | T07. |
| `store/web/` pages | Seller fee, checkout, account purchases, payouts in the console; en + ar. |
| `web/src/pages/Admin/Plugins/StorePlugin.tsx` | Buy / Link this home to buy; en + ar. |

### Tests

- Unit T01, T03, T06; integration T02, T04, T08, T09; role T05; CI T07. `store` job.

### Rollout

Off until the owner picks the payment provider and seller of record at Phase 3 (after written
confirmation that it allows resale of third-party plugins and does not run on Stripe) and opens
the store's own provider account (an owner action; nothing in this epic calls a live payment
API).

### Rollback

Pause paid listings (store admin switch). Existing entitlements keep refreshing; nothing is
deleted.

### Dependencies

54.14 (entitlement key use), 54.15, 54.18; the Phase 3 payment provider decision (README §14
item 2). No dependency on Epic 16's licence issuer or Epic 50's billing.
