# Implementation Plan — Story 16.3 Subscription management

> Companion to [story-16-03-subscription-management.md](story-16-03-subscription-management.md).
> The story states *what* and *why*; this plan states *how*.

> **Payment provider: TBD.** Billing lives in Maktaba Cloud
> (Epic 25, stories 25.13 / 25.14) behind the `billing.Provider`
> interface (`cloud/internal/billing/provider.go`). The only provider
> shipped is `none` (the default): paid plans show "Payments coming
> soon", checkout and the portal answer `503 payments_not_set_up`, and
> service admins set a user's plan by hand
> (`POST /v1/admin/accounts/{id}/plan`). This plan describes the home
> side, which is the same whichever provider ships.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Settings UI | `web/src/features/settings/Subscription.tsx` (canonical); native shells deep-link out to the same web view so we don't duplicate the billing pages. |
| Manage plan | Deep-links to the cloud's billing page; the provider's customer portal (once a provider ships) is one click from there. |
| Webhook receiver | In the cloud: `POST /v1/billing/webhook/{provider}` (25.14). The home never receives payment-provider webhooks. |
| Tier on the home | The cloud turns a paid plan into a signed license (16.4); the home polls for it every minute. |
| Cancellation copy | Localized strings (English + Arabic) under `billing.cancel.*`. |
| Out of scope | Pricing decisions (open question in epic README); Apple/Google IAP (deferred). |

## 1. Flow

```
   user (admin)                 cloud                       payment provider (TBD)
       │                          │                                │
       │ Manage plan              │                                │
       │ ───────────────────────► │  POST /v1/billing/portal       │
       │                          │ ── Provider.CreatePortal ────► │
       │ ◄──────────── { portal_url } (503 under "none")           │
       │                                                           │
       │ change plan / cancel / update payment method              │
       │ ────────────────────────────────────────────────────────► │
       │                          │ ◄── webhook (verified by       │
       │                          │     Provider.ParseWebhook)     │
       │                          │  subscription_changed          │
       │                          │  → plan mirrored, license      │
       │                          │    re-issued                   │
   home│ ── license poll (60 s) ─►│                                │
       │ ◄── new license; flags reload (Story 16.6)                │
```

## 2. Data

The cloud owns all billing tables (`subscriptions`, `invoices`,
`provider_events`; see plan-25-13 §1). Their provider-specific ids are
`provider_customer_id`, `provider_subscription_id`, `provider_price_id`
and `provider_invoice_id`; each row carries the `provider` that wrote
it (`manual` for a plan a service admin set by hand). The home stores
only the license it was issued.

## 3. Webhook events that move the home's tier

Neutral events (25.14):

- `subscription_started` / `subscription_changed`: new plan → new
  license (upgrades immediate).
- `subscription_cancelled`: at period end the account falls back to
  free; the license is not renewed and the home returns to Free.
- `payment_failed`: subscription `past_due`; the payer is mailed.
- `payment_disputed`: the cloud suspends the account; its homes'
  licenses are refused (`license_revoked`) and lock; after the dispute
  the same license comes back and unlocks. No data is destroyed.

Retries and replays are absorbed by the cloud's `provider_events`
ledger; out-of-order events older than the last applied one are
ignored.

## 4. Cancellation flow (UX)

The provider's customer portal handles "downgrade takes effect on next
renewal". The cloud mirrors the flag as `cancel_at_period_end`, and the
UI reads:

```tsx
{sub.cancel_at_period_end && (
    <Banner intent="info">
        Cancels on {fmt(sub.current_period_end)}.
    </Banner>
)}
```

Restoring a cancelled subscription before expiry is done in the portal;
the resulting `subscription_changed` event clears the flag.

## 5. Receipts & VAT

Invoices and tax are the payment provider's; the cloud records each
invoice outcome (`payment_succeeded` / `payment_failed`) and the
Settings → Subscription page links to the provider's receipts. We do
not parse invoice line items.

## 6. Test plan

| Test | What it pins |
|---|---|
| `TestManagePlanDeepLink` | Manage plan opens the cloud billing page. |
| `TestPortalNotSetUp` | Under provider `none`, the portal answers 503 `payments_not_set_up` and the UI says payments are coming soon. |
| `TestManualPlanIssuesLicense` | Admin sets a plan by hand → the home's next poll gets the matching license. |
| `testCancelBannerShows` | `cancel_at_period_end = true` → banner visible. |
| `testUpgradeUIUnlocksWithin60s` | Fake provider delivers `subscription_changed` home → pro; the home's flags refresh within 60 s. |
| `TestDisputeLocksHome` | `payment_disputed` → license refused; home locks; data intact. |

## 7. Edge cases — handling table

| Case | Behaviour |
|---|---|
| Webhook delivery failure | The provider retries; the cloud's ledger makes retries safe. |
| Double purchase (two devices) | Checkout refuses a second subscription (`409 already_subscribed`). |
| Disputed payment | Account suspended; licenses refused; restored after the dispute. |
| User deleted | The cloud cascades billing rows; the provider's customer record is left alone. |
| No provider (`none`) | No purchases possible; plans come only from a service admin's manual set. |

## 8. Acceptance checklist

- [ ] Subscription page shows tier, seats, holder, renewal/expiry, cancel banner.
- [ ] Manage plan deep-links to the cloud billing page.
- [ ] A plan change (webhook or manual admin set) reaches the home within 60 s.
- [ ] Strings ship in English and Arabic.
- [ ] Tests in §6 pass.
