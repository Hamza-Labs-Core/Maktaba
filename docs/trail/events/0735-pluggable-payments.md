---
schema: 1
kind: pr
pr: 735
title: Paid plugins through pluggable payment providers, the store side (Epic 54, story 54.19)
date: 2026-11-06
session: 28c0b780-1dd6-4ba0-b27f-a3382e1c1cd1
prs: [731]
---

## What happened
- Payment providers became pluggable instead of one Phase 3 choice: a `PaymentProvider` interface and a registry in a new `store/` package (TypeScript), a provider-neutral ledger in store-commerce's first D1 migration, store-signed entitlements, refunds, disputes and monthly payouts, a fake and an Adyen for Platforms module (no keys), a CI check that keeps each provider in its own folder, and a `store` CI job. The spec (README §6.5, §8, §14 item 2, story 54.19) records the decision.

## What we learned
- The brief named Stripe Connect as the common choice, but research.md and the trail record no Stripe ever (#591, #593). Check a brief's suggestion against the trail before building it. Adyen for Platforms fits instead: the store is the seller, so the merchant-of-record resale restriction does not apply, and it acquires locally in the UAE and Saudi Arabia.
- `node:sqlite` (Node 22.13+) runs D1's SQL as is, so store-commerce tests need no Miniflare: a 40-line adapter with `batch()` as one transaction.
- A far-future ISO date from `new Date(8.64e15)` starts with `+275760`, which sorts before `2026` as text: compare ISO timestamps in SQL against `9999-12-31T…`, not the largest Date.
- The wiki's Arabic file is `indent=2` JSON; rewriting it with another indent turns a two-line change into 784 lines.

## Open
- The home side of entitlements (offline check against `store-keys.json`, the `entitlement_missing` gate, Buy and Link this home in Browse) follows in its own PR; it needs 54.14's `pluginsig` for the key chain.
- Phase 2's store mounts the webhook route and the sales calls, with role tests (T05) and the paid CDN path (T09).
