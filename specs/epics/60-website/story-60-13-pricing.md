# Story 60.13 — Pricing, from the plans the cloud enforces

> Epic 60 · Phase 3, **gated on Epic 50 deciding its plans** · Depends on: 60.01 part B, 60.02,
> Epic 50's plan data · Owner question: Q8

## Why

Maktaba is free to run at home. Epic 16 gates a few features behind a licence, and Epic 50 makes a
cloud relay people can pay for. A household will want to know what is free and what costs money,
before it installs. Today the plan limits do not agree with each other (Epic 50 gap G13), and no
prices are decided. A page with invented prices would be worse than no page. The project builds a
feature or does not show it.

## Description

**Until Epic 50 decides the plans:**
- there is **no `/pricing/` page** and no link to one;
- the home page's privacy and get-started sections say Maktaba is free to run at home, which is
  true today;
- the link check keeps any `/pricing` link out of the product.

**Once the plans are decided:**
- **The source is the cloud's own plan data,** the same list the cloud enforces (Epic 50's billing
  package and the licence features of Epic 16). It is exported as JSON by a small Go program at
  build time, as the privacy policy is (60.07). The page cannot show a limit the cloud does not
  enforce.
- **The page** (`/pricing/`, `/ar/pricing/`), in 60.01 part B's layout:
  - **Free at home:** everything that runs on your own computer or NAS, with no account.
  - **Each paid plan:** the price in each offered currency, what it adds (remote viewing through
    the cloud, the number of streams, seats), and how to start (a link to app.maktaba.tv's
    sign-up).
  - **Questions:** what happens if you stop paying (your home keeps working; remote access stops),
    and where payment is handled: by Epic 50's pluggable payment providers (`PaymentProvider`),
    named on the page as the cloud configures them.
  - Prices are shown with tax wording per region as Epic 50 decides.
- **The link:** the footer and the home page link to `/pricing/` from the same pull request that
  adds the page.

## Acceptance criteria

- **Given** Epic 50's plans undecided, **then** no `/pricing/` page or link exists, and the link
  check fails a new one.
- **Given** the plans decided, **then** the page's limits and prices equal the cloud's plan data,
  and a change to that data without the page fails CI.
- **Given** the page, **then** it passes 60.04's bar in both languages.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | link check | a `/pricing` link before the page (fixture) | 60.08 | fails |
| T02 | Go + build | plan export vs the built page | compare | equal |
| T03 | build | plan data changed (fixture) without a rebuild | CI | fails |
| T04 | Lighthouse + axe | `/pricing/` × 2 | run | 60.04's bar |

## Edge cases

- **A plan offered only in some regions:** the page shows it with its region line, from the same
  data.
- **A price change:** the page changes in the same pull request as the plan data. Old prices are
  not kept on the page.

## Build plan

| File | Change |
|---|---|
| `cloud/cmd/plans-json/main.go` (new, tiny) | prints the enforced plans |
| `web/packages/website/scripts/pricing.mjs`, `src/pages/pricing/` | the page |
| footer and home templates | the link, in the same pull request |
