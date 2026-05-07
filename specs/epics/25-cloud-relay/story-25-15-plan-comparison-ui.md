# Story 25.15 — Plan comparison & subscription UI

> Epic 25 · Cloud relay · Phase 3 (billing)

## Description

A user-facing surface that explains what cloud features exist, who they
are for, and how to upgrade. Two contexts:

1. **Public marketing page.** `https://maktaba.app/pricing` —
   anonymous-accessible HTML page rendered server-side; the place
   we link to from the website's nav. Pure presentation; reads
   prices from a static JSON published by 25.13.
2. **In-app upgrade screen.** `app.maktaba.app/upgrade` — same
   plan card but with the user's *current* plan highlighted, a
   "What you're using now" box (servers linked, devices, current
   month's relay GB), and one-click checkout (25.13).

UI states:

- **Free user.** Three cards: Free (selected), Pro
  ("Recommended"), Family. Each card lists features, limits,
  prices. CTA on Pro/Family: "Upgrade".
- **Pro user.** Pro is highlighted. CTA: "Switch to Family"
  (proration preview), "Manage subscription" (portal).
- **Family user.** Family is highlighted. CTA: "Manage
  subscription".
- **Past-due.** Red banner: "Your payment failed —
  [Update card]". After 7 days, downgrade triggered (25.14).
- **Suspended.** Banner: "Your account is suspended —
  [Contact support]" with reason if available.
- **Mobile (iOS).** "Manage on web" message with deep-link;
  no in-app purchase per 25.13 v1 decision.

Microcopy for cards:

- Free: "Run Maktaba on your home network. Push notifications.
  Server status. No remote access."
- Pro: "100 GB / month relay. Watch from anywhere. 2 concurrent
  streams."
- Family: "Up to 5 invitees. 500 GB / month. 5 concurrent
  streams."

Translation:

- The marketing page is bilingual EN / AR for v1
  (Arabic-first product); other languages added by community
  PRs.
- Currency display follows the user's locale; formatted via
  `Intl.NumberFormat`.
- Stripe checkout is in user's accept-language preference; we
  don't override.

Accessibility:

- All cards keyboard-navigable; CTA buttons announce price
  changes when toggling monthly/yearly.
- Color contrast WCAG AA; "Recommended" badge has both color
  and text affordances.

## Acceptance criteria

- **Given** an anonymous user visits `/pricing`,
  **when** the page loads,
  **then** the response renders three plan cards, prices,
  feature lists, and a "Sign up" CTA on each.
- **Given** a Free user visits `/upgrade`,
  **when** the page loads,
  **then** "Free" is highlighted and the page shows their
  current usage (servers linked, devices, this month's GB).
- **Given** a user toggles monthly ↔ yearly,
  **when** the toggle fires,
  **then** prices update without page reload and the savings
  badge appears on yearly.
- **Given** a user clicks "Upgrade to Pro",
  **when** the API call succeeds,
  **then** the browser navigates to the Stripe URL.
- **Given** a Pro user clicks "Switch to Family",
  **when** the proration preview loads,
  **then** the dialog shows "You'll be charged $X today,
  prorated for the remainder of the period" and a confirm
  button that completes the change in Stripe Portal.
- **Given** an iOS app user opens `/upgrade`,
  **when** the page loads,
  **then** the upgrade CTA is replaced with a "Manage on
  web" message (App Store v1 compliance).
- **Given** the user is past-due,
  **when** they visit `/upgrade`,
  **then** a red banner with "Update payment method" appears
  above the cards.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | snapshot    | render free user | snapshot match | matches |
| T02 | snapshot    | render Pro user | snapshot | matches |
| T03 | snapshot    | render past-due | snapshot | red banner present |
| T04 | a11y        | tab order | navigate cards | all reachable, focus visible |
| T05 | a11y        | high contrast | render | 4.5:1 minimum text contrast |
| T06 | i18n        | render in `ar` | snapshot | RTL layout, Arabic numerals optional |
| T07 | unit        | yearly savings calc | $9.99 × 12 vs $99 | "Save $20.88 / year" |
| T08 | regression  | iOS UA | render upgrade | no in-app CTA |
| T09 | integration | click Upgrade | follow redirect | lands on Stripe checkout |
| T10 | integration | apply promo code in dialog | preview | discount line visible |

## Edge cases

- **Currency confusion.** When user's locale is `de-DE`
  but card was last charged in USD, show USD with hint
  "billed in USD"; we don't convert client-side.
- **Mid-cycle plan change.** Stripe handles proration; we
  show the upcoming invoice line items.
- **Trial.** Out for v1.
- **Expired free tier.** Free tier never expires; no
  hard-down state.
- **Page caching.** `/pricing` cached 5 min at Cloudflare;
  changes propagate within 5 min. Pre-warm cache on deploy
  to avoid stampede.
- **PI exposure.** No PII in `/pricing`; in `/upgrade` we
  show usage (servers, GB) but never billing card data
  (Stripe does that).
- **Server-rendered prices.** Prices come from a static
  JSON `https://api.maktaba.app/api/billing/plans` that
  the marketing page fetches at build time and at request
  time as a fallback. If the JSON fetch fails, we show
  the last known prices (cached 24h).
- **Anti-fingerprinting.** No third-party trackers on
  `/pricing` (no analytics scripts that exfiltrate
  identity); first-party plausible-style aggregate counters
  only.

## Files / packages

- `web/src/pages/Pricing.tsx` — marketing.
- `web/src/pages/Upgrade.tsx` — in-app.
- `web/src/components/billing/PlanCard.tsx`.
- `web/src/components/billing/CurrentUsage.tsx`.
- `web/src/lib/billing.ts` — API client.

## Open questions

- **Trial periods.** Add 14-day trial for Pro? Defer to
  growth experiment after launch.
- **Annual discount %.** 17% currently; rerun pricing
  experiment in 6 months.
