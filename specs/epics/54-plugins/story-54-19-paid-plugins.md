# Story 54.19 — Paid plugins

> Epic 54 · Phase 3 · Depends on: 54.14, 54.15, 54.18, 54.37 ·
> Needed by: —

## Why

Developers maintain what pays (Plex's plugins faded partly because nobody was paid to keep
them working). Both free and paid plugins are required. The store sells them **on its own**:
its own payment provider accounts, its own payouts, tax records and refunds, and its own
signed entitlements that homes check offline. Nothing here uses Maktaba
Cloud's billing (Epic 50.19), its licence issuer or its accounts, and no home calls the cloud to
use a paid plugin.

Decisions of 2026-11-03 that shape this story:

- **The store is independent of Maktaba Cloud**, payments included (54.15 "Why independent").
- **Split:** the developer gets 85 %, Maktaba keeps 15 % of the price.
- **Developer fee:** free to publish free plugins; a one-time $25 to sell.
- **Payment providers are pluggable** (decided 2026-11-06, README §14 item 2). No single
  provider is chosen: the store runs providers behind one `PaymentProvider` interface, several
  at once, and adding one is a module and its configuration.

## Description

### 1. Pluggable payment providers

The store resells third-party products, which many sellers of record restrict (Paddle among
them), and no one provider serves every region and currency the store sells in. So the
store does not wait for one choice: payment providers are pluggable, and a provider is used
live only after it confirms in writing that it allows plugin resale. Every provider account
is the store's own, opened for the store and separate from any account used for cloud
billing (separate merchant account, API keys, webhook secrets and payout bank details).

A provider follows one of three models, named in its capability descriptor, and the store
can run providers of different models at once:

| # | Model | Who sells | Who files VAT and sales tax | How developers are paid | Candidates |
|---|---|---|---|---|---|
| A | **A merchant of record sells for the store** (`merchant_of_record`) | the provider | the provider | the provider pays the store; the store pays each developer their 85 % monthly (`store_batches`) | Paddle, FastSpring, 2Checkout (Verifone) |
| B | **The store is the seller** with a marketplace provider (`marketplace`) | the store's legal entity | the store | the provider splits each payment: 85 % to the developer's sub-account, 15 % to the store (`provider_splits`) | Adyen for Platforms, PayPal (marketplace), Checkout.com; for the Gulf and Egypt, Tap Payments, PayTabs or Paymob |
| C | **Developers sell directly** (`developer_direct`) | the developer | the developer | directly; the store invoices its 15 % monthly and verifies the developer's licence keys offline | none needed for sales; the store's 15 % invoices and the $25 fee go through an A or B provider |

The first two modules are `fake` (CI and staging; its configuration plays model A or B) and
`adyen` (Adyen for Platforms, model B: hosted Checkout sessions with splits to each
developer's balance account, HMAC-signed webhooks, the store files tax; local acquiring in
the UAE and Saudi Arabia, SAR, AED and EGP among its currencies). Neither is wired to live
keys. A further provider, a Gulf or Egypt one for example, is a module beside them.

### 2. The `PaymentProvider` interface and the registry

`store/payments/provider.ts`:

| Member | What it does |
|---|---|
| `capabilities` | the descriptor: `id`, `model` (A, B or C above), `payouts` (`provider_splits`, `store_batches`, `none`), `subscriptions`, `refunds` (full or partial), `disputes`, `tax` (who calculates it), `regions` (buyer countries), `currencies` |
| `onboardDeveloper(dev, returnUrl) → {url, accountRef}` | starts the provider's identity and payout onboarding (A: the store's own payout form; B: the provider's sub-account onboarding; C: registering the developer's licence-key public key) |
| `payoutStatus(accountRef) → active / pending / restricted` | whether the developer can sell through this provider |
| `createCheckout(request) → {providerRef, url, expiresAt}` | a hosted checkout for the price with its split, tax included where the provider or the store is seller |
| `parseWebhook(request) → ProviderEvent[]` | verifies the webhook's signature with the provider's secret before reading it, and answers what it said in the store's words: payment succeeded or failed, refund succeeded or failed, dispute opened, won or lost, payout paid or failed, account updated; `webhookAck()` is the body the provider expects back |
| `refund(request)` | a refund, reversing the developer's share |
| `payout(instruction)` | one developer's monthly payout (`store_batches` providers only) |
| `quoteTax?(price, country)` | the tax hook: the provider's tax on a price, when it calculates tax |
| `submitDisputeEvidence?(ref, evidence)` | the dispute flow: the store's purchase, entitlement and download records |

A module (`store/payments/providers/<id>/`) exports its id, the Worker secrets it needs
(`PAYMENTS_<ID>_<NAME>`), a parser for its settings and a factory; it also has a
`provider.json` listing its markers (SDK packages, API hosts, key prefixes). Adding a
provider is that folder, one line in `payments/providers/index.ts` and the store admin's
configuration. Nothing outside the folder names a provider.

**The registry** (`store/payments/registry.ts`) reads `payment_providers` (store-commerce):
one row per provider with `enabled`, `priority`, `mode` (`test` or `live`), the buyer
`regions` and `currencies` the store uses it for, `resale_confirmed_at` and its non-secret
`settings`. A sale goes to the first enabled provider, in the admin's order, that sells to the
buyer's country in the price's currency (and renews, for a yearly product, and, for a
`provider_splits` provider, holds an active account for the developer). A `live` row without
`resale_confirmed_at`, or without its secrets, is not used and the store admin page says why.
A purchase, its refund, its disputes and its payouts stay with the provider that took it.

**One ledger.** `purchases`, `ledger_entries` (every movement, signed, per party: `maktaba`
or `developer:<id>`), `provider_events`, `payouts`, `entitlements`, `seller_fees`,
`provider_accounts`, `products` and `prices` are the store's own (store-commerce migration
`0001_payments.sql`); each provider's records are mapped into them as `ProviderEvent`s. The
home never sees a provider: it sees only entitlements the store signs (§6).

Webhooks go to the `store-web` Worker at `POST plugins.maktaba.tv/v1/payments/webhook/<provider>`:
verified by the provider's module with its signing secret (a Worker secret), idempotent on
the provider's event id (`provider_events` key `(provider_id, event_id)`), and answered `200`
only after the D1 write commits, so the provider retries anything lost. A bad signature is
`401` and nothing is stored. Provider secrets are set by the owner as Worker secrets, never in
files or CI logs.

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
  `store-commerce`. Prices in USD, EUR, GBP, SAR, AED, EGP where an enabled provider charges
  in them, set by the developer within store limits ($1–$200 or equivalent).
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
3. The registry picks the provider for the buyer's country and currency, and the store calls
   its `createCheckout`. With A and B the buyer pays on the provider's hosted page; with
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
  the same numbers as Epic 16's, in `shared/plugins/pluginsig` (`CheckEntitlement`).
- The home refreshes entitlements daily with a signed request to the store. A store outage
  shorter than 31 days changes nothing for a paying home; a cloud outage of any length changes
  nothing at all.
- With model C, the entitlement is the developer's own licence for the home, signed by the
  developer's registered licence-key public key and checked offline the same way; the store
  countersigns the developer's public key, not each licence.
- A one-time purchase renews forever while the purchase is not refunded.
- **No valid entitlement** → the plugin is *Waiting* `entitlement_missing`: queue kept, data
  kept. This is not Epic 51's licence gating: a component's `licence` names one existing flag
  of `api/internal/flags/declarations.go` (today only `cloud.relay` uses one, `cloud_relay`), and
  a plugin entitlement is neither a flag nor part of the home's licence. The entitlement is a
  gate of its own (54.37 §3), with the reason code `entitlement_missing` added to `reasons` in
  `shared/components/catalog.toml`; the runners treat it like `licence_missing`. It never deletes data and never blocks other plugins. Words: "Waiting
  for this plugin's licence" / «في انتظار ترخيص هذه الإضافة».
- **Packages of paid plugins** are downloaded from `plugins-cdn.maktaba.tv/paid/<sha256>` with
  a signed URL (`t`: home id, sha256, expiry 10 minutes, signed by the store) issued only
  against a valid entitlement; the `store-cdn` Worker checks it and streams from the private
  R2 prefix. A copied package installed on another home installs but stays *Waiting*
  `entitlement_missing`.
- **Moving to a new home:** the buyer's store account can move a purchase to another of its
  linked homes (twice a year, re-auth), which ends the old home's entitlement at its next
  refresh.
- **As built.** An entitlement is a signed envelope of kind `entitlement` (54.14's format and
  message), times in RFC 3339 without fractions, `home_key_fp` the sha256 (hex) of the home
  store key's raw public key. The store answers `GET /v1/homes/{id}/entitlements` to the home's
  own MKS-Home signature, from `store-commerce`, signed with `STORE_ENTITLEMENT_KEY`. On the
  home, the API's refresher (`api/internal/plugins/entitlements.go`) fetches daily (an hour of
  jitter) and, after a Buy, every 15 s for 15 minutes; the supervisor checks each envelope
  against the store keys it verified (`POST /plugins/entitlements/verify`, `pluginsig.
  CheckEntitlement`); the ones that hold replace `plugin_entitlements` (migration 0201), and
  any failure keeps the rows. Both gates (the supervisor's runner, the pipeline's
  `plugingate`) read only `valid_until`: a manifest whose `price` is not `free` waits
  `entitlement_missing` without a row valid now (72-hour grace). Settings → Plugins → Store
  account lists the purchases held and has **Check for purchases now**. Not built yet: the
  `/paid/*` CDN path and moving a purchase to another home.

### 7. Refunds, disputes and tax: owned by the store

- **Refunds** within 14 days on request in the store account page, automatic through the
  provider that took the sale (`refund`)
  (the developer's share is reversed, or for A deducted from the next monthly payout); the
  entitlement ends at the next refresh. Later refunds are a `store_admin` decision.
- **Disputes:** a lost dispute ends the entitlement and reverses the developer's share the
  same way.
- A plugin `disable`-revoked for malware: buyers are refunded automatically; the developer's
  share is reversed.
- **Tax:** with an A provider the merchant of record files; with a B provider the store's
  entity registers and files (the provider's tax calculation where it has one, through
  `quoteTax`); with C the developer does. In every model the
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
  **then** the plugin is *Waiting* `entitlement_missing`, its queue and data kept; a new
  entitlement brings it back with nobody touching it.
- **Given** an entitlement for another home id, or for this home id with another home key,
  **when** verified,
  **then** it is refused.
- **Given** a refund within 14 days,
  **when** processed,
  **then** the developer's share is reversed and the entitlement ends at the next refresh.
- **Given** a paid plugin's package copied to another home,
  **when** installed there,
  **then** it stays *Waiting* `entitlement_missing`.
- **Given** any change under `store/`,
  **when** CI runs,
  **then** the payment-provider check finds no payment SDK import, payment API host or payment
  key prefix outside the module of a provider that declares it, and nothing names Stripe or a
  Stripe-owned service anywhere.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (ts) | split function, shared table | 85/15 on prices and currencies | Maktaba rounded down, developer the rest, sums equal |
| T02 | integration (ts, Miniflare D1) | fake provider events: paid, refunded, disputed, payout failed, duplicates | process | purchase rows, reversals, idempotent |
| T03 | unit (go home + ts store) | entitlement signed by E (ts) and verified (go); model C developer key | verify | valid / expired / other home / other home key / wrong key use |
| T04 | integration (PG, go home) | lapse past grace; cloud and store hosts blackholed | runner | runs while valid; Waiting `entitlement_missing` after; back on renew |
| T05 | role tests (ts) | seller fee, pricing, purchase, refund, move, sales, payout approval routes | roles | developer of other plugin, buyer of another account, unlinked home, cloud token refused; payout approval needs `store_admin` + re-auth |
| T06 | unit | strings en + ar | checkout, account, link-to-buy and console pages; `entitlement_missing` words | complete |
| T07 | CI check (`tools/ci/payment_provider_allowlist.py`) | `store/` tree and its lockfiles | each provider's `provider.json` markers and a list of known payment SDKs and hosts | each only inside its provider's module; Stripe nowhere |
| T10 | unit (ts) | the registry: providers with regions, currencies, priorities, live without resale confirmation, missing secrets | choose | the admin's order per country and currency; unused rows say why |
| T11 | unit (ts) | the Adyen module with recorded request and notification shapes | checkout, webhook HMAC, refund, onboarding | splits sum to the price; a changed field or another merchant account is refused |
| T08 | integration (ts) | model A monthly payout run on the fake | `payouts(month)` | 85 % of net sales per developer after refunds; under $25 rolls over |
| T09 | integration (ts) | `/paid/*` with an expired, a forged and another home's `t` | GET | 403 each; valid one streams |

All payment tests use the fake provider and recorded fixtures; no live payment calls
(standing rule).

## Edge cases

- **A buyer's country no enabled provider sells to**: Buy is disabled with an explanation;
  free plugins are unaffected.
- **A developer's payout account restricted** by the provider: new sales paused; existing
  entitlements continue.
- **Price 0 promotional**: not supported; promotion codes only where the provider has them.
- **A provider is added, reordered or turned off**: existing entitlements are the store's own
  signatures and keep working; refunds and disputes of earlier sales stay with the provider
  that took them while it is enabled; only new purchases follow the new order.
- **The entitlement key E is rotated**: the new key is listed in `store-keys.json` first; homes
  get new entitlements at the next daily refresh; old ones stay valid to their `valid_until`.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `store/payments/provider.ts`, `registry.ts` (new) | §2 interface and the registry. |
| `store/payments/providers/fake/`, `providers/adyen/` (new) | The fake and the Adyen for Platforms module, each with `provider.json`. |
| `store/commerce/split.ts`, `ledger.ts`, `sales.ts`, `entitlements.ts` (new) | §3–§7. |
| `store/web/payments_webhook.ts` (new) | Provider events. |
| `store/cdn/paid.ts` (new) | Signed URLs for paid packages. |
| `store/migrations/commerce/0001_payments.sql` (new) | Providers, provider accounts, seller fees, products, prices, purchases, the ledger, provider events, payouts, entitlements. |
| `api/internal/plugins/entitlements.go` (new) | The home's refresh (store → supervisor check → `plugin_entitlements`). |
| `cmd/maktaba-server/internal/plugins/routes.go`, `runner.go` | The supervisor's check route and the `entitlement_missing` gate (54.37); `pipeline/.../components/plugingate.py` the same gate. |
| `shared/db/migrations/0201_plugins_entitlements.sql` (new) | `plugin_entitlements`. |
| `shared/components/catalog.toml`, `web/src/i18n/{en,ar}.json` | `entitlement_missing` in `reasons`, with its words (if 54.37 has not added it). |
| `shared/plugins/pluginsig/` | Entitlement verification with `use: "entitlement"` keys (`VerifyEntitlement`, `CheckEntitlement`). |
| `tools/ci/payment_provider_allowlist.py` (new) | T07. |
| `store/web/` pages | Seller fee, checkout, account purchases, payouts in the console; en + ar. |
| `web/src/pages/Admin/Plugins/StorePlugin.tsx` | Buy / Link this home to buy; en + ar. |

### Tests

- Unit T01, T03, T06, T10, T11; integration T02, T04, T08, T09; role T05; CI T07. `store` job.

### Rollout

Each provider is off until the store's own account with it is opened, its written
confirmation that it allows the resale of third-party plugins is recorded
(`resale_confirmed_at`), its Worker secrets are set and the store admin enables it (nothing
in this epic calls a live payment API). The steps per provider are in
`store/payments/README.md`.

### Rollback

Pause paid listings (store admin switch). Existing entitlements keep refreshing; nothing is
deleted.

### Dependencies

54.14 (entitlement key use), 54.15, 54.18. No single payment provider (README §14 item 2:
pluggable). No dependency on Epic 16's licence issuer or Epic 50's billing.
