---
schema: 1
kind: pr
pr: 1284
title: "Copyright notices, reports and takedowns for broadcast channels (story 50.24)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1284 merged as b76b1aa18, after #1274 and #1270, so home migration 0229 followed 0228 and
  0227. Anyone can file a copyright notice or report a published channel (rate-limited,
  Turnstile, each required element named); an urgent report pages on call. A service admin
  decides each one in the console's Notices and reports queue, always with a reason, a fresh MFA
  for takedown, restore and similar acts, and an audit row. A takedown reaches the home as a
  signed instruction it pulls each minute; the home drops the item from its published channels,
  the edge shows the slate, and the home acks. The publisher gets a statement of reasons in
  English and Arabic and can counter-notify; the item can return after 10 business days unless
  the claimant sues. Three strikes in 12 months end broadcasting through a `terminate` entry on
  50.25's revocation list. Cloud migration `00500025`; records kept 3 years.
- Every ingest-token request now names the channel's `cloud_id`; the speed test gets its own
  token type, accepted only on `/v1/ingest/speedtest`.

## What we learned
- A diff over GitHub's 20,000-line limit makes `gh pr diff --name-only` fail; use
  `git diff --name-only origin/main...<head>` for the trail-file and migration checks.
- A watcher's migration check must compare the PR's added files (`git diff --name-status
  origin/main...<head> -- cloud/migrations/`, lines A) with main's listing, never a mixed
  listing: one watcher misread main's 00500021 as the PR's.
- Requiring `cloud_id` from the start was safe only because the cloud has never been deployed;
  after a deploy, such a contract change needs a compatibility window.

## Open
- Owner actions: the DMCA agent registration, counsel review, the copyright@ mailbox, Turnstile
  hostnames, manual billing cancellation, the `broadcast_urgent_report` alert route.
