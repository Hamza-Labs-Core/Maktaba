---
schema: 1
kind: pr
pr: 1238
title: "Epic 50 story 50.20: the Broadcaster tier and guest sharing"
date: 2026-11-14
session: 058ba18a-4595-4f77-b516-82416b30dabb
agent: claude-code
---

## What happened
- #1238 merged as fb155c354. Broadcasting's plans, metering and billing, all shut behind
  `broadcast.enabled = false` (refused at start while any price is TBD):
  - Pro carries guest sharing (1 invite-only on-demand channel, 5 viewers, 50 viewer-hours,
    2 rungs); Broadcaster is a per-home add-on to Pro (3 channels, 100 viewers, 5,000
    viewer-hours, 3 rungs, pay-as-you-go, extras). Catalog in
    `cloud/internal/billing/broadcast_catalog.go`, nil prices meaning TBD.
  - Modes for new sessions (`BroadcastModeFor`): full, lowest past the allowance and cap up to
    twice the allowance (bought minutes not counted), wait, off. Playing viewers keep theirs.
  - The edge API for 50.21 (`cloud/internal/handlers/edge`): Ed25519-signed
    `POST /_edge/usage` (5-minute windows, idempotent by `<publisher>:<window>`, states in the
    reply) and `GET /_edge/publishers/{id}`.
  - Publishing checks plan, gate, limits and the Broadcaster agreement; owner-only billing
    routes; hourly viewer-hour outbox; staff grant behind fresh MFA; licence feature
    `broadcast`; cloud billing page per home in English and Arabic.
  - Migration `00500017_broadcast_plans`; rehearsal C20 `broadcast-plans` (the test plays the
    edge against the plans-build api).
- Main moved three times while the pull request was open; the migration slot went 15 → 16 →
  19, then another session moved it to 17 because the open #1251 had claimed 19.

## What we learned
- Taking the next free slot on main is not enough while other pull requests are open: an open
  pull request can already hold a later number. Check open pull requests' migrations too
  (`gh pr list` and their diffs) before pushing the final slot.
- `affected-tests` asks for a changed test beside every changed source file, even a one-line
  addition to a route list or a flag list; pin the new entry in its test in the same push.
- The runbooks lint keeps the Arabic word رصيد (credit) for `uptime.policy` only; billing copy
  says حصة (allowance).
- The integration job's `TestIngestUpsertAndForget` can fail on a Postgres i/o timeout at the
  migrator lock; it passed on the next run.

## Keep doing
- A rehearsal scenario that plays the other side (here the edge) pins the contract before that
  side exists; the later story swaps its real component in.

## Stop doing
- Pushing a migration slot without checking open pull requests' slots.

## Open
- Owner actions: the Broadcaster (and Pro) prices; the provider's prices (never Stripe); an
  Ed25519 edge key, its private half the 50.21 Worker's secret and its public half
  `broadcast.edge_public_key`; then `broadcast.enabled`.
- Android `values-ar` and Apple catalog strings for broadcasting come with their screens
  (50.22, 50.26, 50.27). The website pricing page (60.13) can now list Broadcaster.
- A staff Broadcaster grant on a Pro account is replaced by the next subscription webhook that
  carries broadcast items; the test-publisher flag is the lasting staff lever.
