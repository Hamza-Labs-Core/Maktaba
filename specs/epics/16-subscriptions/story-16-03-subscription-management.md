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
