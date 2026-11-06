---
schema: 1
kind: pr
pr: 593
title: Stripe removed everywhere; provider-neutral billing
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 1d5bd9a07, per the no-Stripe rule; payment provider TBD.
- Cloud billing goes through a `billing.Provider` interface (checkout, portal, verify+parse webhook into neutral events). The only shipped provider is `none` (default `[billing] provider = "none"`; unknown providers fail config validation): paid plans show "Payments coming soon" in English and Arabic, checkout and portal return 503 `payments_not_set_up`, and `/v1/billing/webhook/{provider}` returns 404.
- Service admins set plans by hand via `POST /v1/admin/accounts/{id}/plan` (reason required, audited, not revenue, 409 while a provider bills) and a Set plan dialog in the web console.
- Cloud migration 00160001 (`00160001_billing_provider_neutral.sql`) renames the Stripe-named billing columns and ledger to `provider_*` / `provider_events` and adds a `provider` column (up/down tested).
- Remaining grep hits: old migration SQL, the 0016 rename statements, StripEpisode, visual stripes, "never Stripe" lines in Epics 50/54, and the trail.
