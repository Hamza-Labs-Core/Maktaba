# Story 54.19 — Paid plugins

> Epic 54 · Phase 3 · Depends on: 54.15, 54.18 · Needed by: —

## Why

Developers maintain what pays (Plex's plugins faded partly because nobody was paid to keep
them working). The owner asked for free and paid plugins. Maktaba Cloud already takes payments
with Stripe and issues signed licences homes check offline; paid plugins reuse both, with
Stripe Connect so developers are paid directly.

## Description

### 1. Developers get paid

- In the console, **Set up payouts** opens Stripe Connect onboarding for an **Express** account
  (Stripe handles identity checks and payouts). The store keeps only the connected account id
  and its status.
- A developer can price a plugin only after payouts are active and (owner question 3) the
  one-time seller fee is paid.

### 2. Products and prices

- `store_products (plugin_id, kind ("one_time" | "yearly"), price per currency, active)`.
  Prices in USD, EUR, GBP, SAR, AED, EGP (Stripe Checkout's local currencies), set by the
  developer within store limits ($1–$200 or equivalent).
- A price change applies to new purchases; yearly renewals keep the old price for the current
  term.
- Free plugins stay free: a listed free plugin cannot become paid (a new plugin id is needed),
  so owners are not surprised.

### 3. Buying

1. The admin clicks **Buy** in the home's Browse (or on maktaba.tv). The home opens the
   maktaba.tv checkout page for the signed-in Maktaba account and the home id (linked homes;
   unlinked homes buy on maktaba.tv and enter the home's claim code).
2. maktaba.tv creates a Stripe Checkout session with the cloud's existing client
   (`cloud/internal/billing/stripe.go` gains `payment_intent_data[application_fee_amount]` and
   `payment_intent_data[transfer_data][destination]`, i.e. destination charges), for the price
   with **Stripe Tax** if Maktaba is merchant of record (owner question 2).
3. The webhook (`cloud/internal/handlers/billing/webhook.go`) records `store_purchases
   (account, home, plugin, product, amount, fee, status, period_end)` on success.
4. The home downloads the package and the **entitlement**.

**Split:** `application_fee_amount = 15 %` of the price (owner question 1); Stripe's processing
fee comes out of Maktaba's share.

### 4. Entitlements

- The cloud's licence issuer (`cloud/internal/license/issuer.go`) signs a **plugin entitlement**
  `{home_id, plugin_id, product, valid_from, valid_until (purchase: +31 days rolling; yearly:
  period end), refreshed_at}` with the existing licence key; homes verify with the existing
  licence verifier.
- The home refreshes entitlements daily with its licence refresh (the same grace rules: 31 days
  plus `SkewGrace`). A one-time purchase renews forever while the purchase is not refunded.
- **No valid entitlement** → the plugin is *Waiting* `licence_missing` (Epic 51 §4.10): held,
  queue kept, data kept. It never deletes data and never blocks other plugins.
- Packages of paid plugins are downloaded with a short-lived signed URL issued against a valid
  entitlement. A copied package installed on another home installs but stays *Waiting*
  `licence_missing`.
- Moving to a new home: the buyer's account can move a purchase to another home of theirs
  (twice a year), which revokes the old home's entitlement at its next refresh.

### 5. Refunds and disputes

- Refund within 14 days on request in the account page, automatic (Stripe refund with
  `refund_application_fee=true` and `reverse_transfer=true`); the entitlement ends at the next
  refresh.
- Disputes: the existing dispute handling looks up the customer; a lost dispute ends the
  entitlement.
- A plugin `disable`-revoked for malware: buyers are refunded automatically; the developer's
  transfer is reversed.

### 6. Plugins never touch money

No capability, API call, panel or string can show a price, a "buy" link or a paywall. The
listing is the only place prices appear (review guideline, and the no-URL rules already stop
links).

## Acceptance criteria

- **Given** a developer without active payouts,
  **when** they set a price,
  **then** `403 payouts_required`.
- **Given** a $10 purchase,
  **when** Stripe confirms (test fixtures),
  **then** the developer's connected account receives the transfer minus the $1.50 application
  fee, the purchase row exists, and the home gets a valid entitlement.
- **Given** an entitlement that lapsed past grace,
  **when** the home's runner passes,
  **then** the plugin is *Waiting* `licence_missing`, its queue and data kept; a new
  entitlement brings it back with nobody touching it.
- **Given** a refund within 14 days,
  **when** processed,
  **then** fee and transfer are reversed and the entitlement ends at the next refresh.
- **Given** a paid plugin's package copied to another home,
  **when** installed there,
  **then** it stays *Waiting* `licence_missing`.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (go, cloud) | Stripe client with fake API base | checkout session | Connect params present; amounts right |
| T02 | integration (PG, cloud) | webhook fixtures: success, refund, dispute | process | purchase rows, reversals |
| T03 | unit (go) | entitlement sign/verify with the licence key | verify | valid / expired / other home |
| T04 | integration (PG, go home) | lapse past grace | runner | Waiting `licence_missing`; back on renew |
| T05 | role tests (cloud) | pricing, purchase, refund, move routes | roles | developer of other plugin, buyer, other user |
| T06 | unit | strings en + ar | checkout and account pages | complete |

All Stripe tests use the fake API base and recorded fixtures; no live Stripe calls (standing
rule).

## Edge cases

- **Currency of a home in a country Stripe can't sell to**: Buy is disabled with an explanation;
  free plugins are unaffected.
- **A developer's Connect account restricted** by Stripe: new sales paused; existing
  entitlements continue.
- **Price 0 promotional**: not supported; use a coupon (Stripe promotion codes) instead.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/internal/billing/stripe.go` | Connect onboarding link, destination charge params, refunds with reversals. |
| `cloud/internal/handlers/billing/webhook.go` | Store purchase events. |
| `cloud/internal/store/products.go`, `purchases.go` (new) | §2–§5. |
| `cloud/internal/license/issuer.go` | Plugin entitlement claims. |
| `api/internal/plugins/entitlements.go` (new) | Home refresh and checks; *Waiting* `licence_missing`. |
| `cloud/migrations/NNNN_store_payments.sql` | Products, purchases. |
| Cloud web app | Checkout, account purchases, payouts in the console; en + ar. |

### Tests

- Unit T01, T03, T06; integration T02, T04; role T05. `store` job.

### Rollout

Off until the owner decides the split, the merchant-of-record question and sets up Stripe
Connect on the live account (an owner action; nothing in this epic calls live Stripe).

### Rollback

Pause paid listings (store admin switch). Existing entitlements keep refreshing; nothing is
deleted.

### Dependencies

54.15, 54.18; Epic 25 billing; Epic 16 licences.
