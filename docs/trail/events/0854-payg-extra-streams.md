---
schema: 1
kind: pr
pr: 854
title: Pay as you go and extra streams on Pro, usage reports (story 50.19, part 3)
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [832, 849]
---

## What happened
- Story 50.19, part 3 of 4:
  - the provider seam's usage reports and items 2 and 3, with `billing.Fake`;
  - pay-as-you-go per account with its cap (`payg_settings`), read by the relay's meter;
  - extra streams on each home (`servers.extra_streams`, `tier_changed` on change);
  - the hourly reporter with its outbox (`billing_usage_outbox`), clamp, notices and daily reconciliation;
  - the API, and the cloud Billing page in en+ar.

## What we learned
- To give the relay a per-home number within a second, put it on the row the relay already caches and extend that row's notify trigger (`CREATE OR REPLACE FUNCTION` is expand-safe), rather than adding a second cache.
- Clamp billing at the report, not at the meter. The meter's mode can lag a flush (30 s), so the reporter caps an account's reported GB at its cap's worth. An overshoot is then never billed.
- An idempotency key per home and hour lets a queued report go out days later without being counted twice; the fake provider proves it by taking a repeated ID once.
- The cloud web app's API client has no PUT; settings changes use PATCH there.
- From #845's integration budget: main after #781 ran in 3m09s (api 189s), against #845's 5m15s (api 315s, the router package 98s against 44s). The job's time varies by runner, close to its 5-minute budget (the Epic 53 delegate's fix).

## Open
- Part 4: the home's cap and license names, the apps' strings, the `relay-plans` rehearsal. The provider and prices are on the go-live list.
