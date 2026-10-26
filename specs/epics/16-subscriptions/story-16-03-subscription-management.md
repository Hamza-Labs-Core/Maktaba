# Story 16.3 — Subscription management

The user can manage their subscription from Settings → Subscription:
view tier, expiry, payment method, change tier, cancel.

**Anchors:** [`architecture.md` §10.4](../../architecture.md).

## AC

- Read-only by default; "Manage" deep-links to the billing portal
  (Stripe Customer Portal or equivalent).
- Cancellation flow: confirm modal with "downgrade takes effect on next
  renewal" copy.
- Upgrades are immediate; downgrades take effect at renewal.
- Receipts: list of past invoices with downloadable PDF (Stripe-issued).
- VAT / tax indication where applicable.

## TC

- Upgrade home → pro: features unlock within 60 s of webhook arrival.
- Cancel: the UI shows "Cancels on 2026-06-01"; daily reminder is off
  by default.
- Restore a cancelled subscription before expiry: feature parity preserved.

## EC

- Webhook delivery failure: we retry with backoff; reconcile via daily
  cron against Stripe's source of truth.
- A user double-purchases on two devices: server dedupes by Stripe
  customer ID; the second purchase is refunded automatically.
- Disputed payment: license tier flips to `free` until resolved; no
  data is destroyed.

## Implementation (2026-10-26)

**Status: done.** The billing itself is Maktaba Cloud's (Epic 25: Stripe
Checkout, the customer portal for payment method, cancellation, receipts and tax,
webhooks with ordering and retries; see
[`cloud/docs/accounts-billing-admin.md`](../../../cloud/docs/accounts-billing-admin.md)).
This epic connects it to the home:

- Settings → Subscription shows the tier, users against seats, the license's
  holder and renewal/expiry date, and **Manage plan** deep-links to the cloud's
  billing page (the portal is one click from there).
- A paid plan becomes a license for each of the owner's homes; the home asks for
  it every minute, so an upgrade unlocks within 60 s of the webhook and a
  cancellation that reaches its end returns the home to Free.
- Disputed payment: the cloud suspends the account; its homes' licenses are
  refused (`license_revoked`) and lock; after the dispute the same license comes
  back and unlocks.
- The cloud's plan cards say which home plan each gives (`home_tier`).
- Cloud plan names predate this epic: `pro` gives the home **Home**, `family`
  gives **Pro** (`license.TierForPlan`). Renaming the cloud catalog is a product
  decision left open.
