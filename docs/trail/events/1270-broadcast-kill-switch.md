---
schema: 1
kind: pr
pr: 1270
title: "The broadcast kill switch (story 50.25)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1270 merged as db709c0ff, after #1263 (it carries #1263's home migration 0227, so it was
  stacked on it). A service admin stops a programme, a channel, a publisher, a home or every
  broadcast from the console's kill switch page, with a reason, a fresh MFA check and an audit
  row. The cloud signs one revocation list (cloud migration `00500022`) and pushes it to the edge
  at once, with a pull every minute as backup; viewers of a stopped channel are refused at their
  next playlist and the home stops uploading. The home keeps the newest signed copy, refuses an
  older one and shows why on its Broadcast page; after two days without a check, published
  channels keep running but a new publish waits. Two edge variables stop one channel or
  everything with the cloud API down.
- The cloud records each list the edge takes (push accepted or the edge's own pull) through
  `broadcast.RecordRevocations`, which feeds 50.28's stale-list alert. 50.26/50.27's watch pages
  now use the kill switch's verdicts; a stopped channel writes `blocked revoked` once.

## What we learned
- Stacking is decided by content, not by the PR description: #1270 was briefed as independent
  but held #1263's migration. Check `git log origin/main..<branch> -- shared/db/migrations/`
  before deciding a PR can merge on its own.
- Story tests can break only after their siblings merge: 50.23 made the Broadcast panel do two
  reads, and 50.25's tests mocked every read with one view. Mock per URL (`serve()`), never with
  a blanket `mockResolvedValue`, in components that may gain a fetch.
- Each sibling merge re-conflicted the remaining stack in CHANGELOGs, string tables and the edge
  Worker. Merge each stack PR the moment it is green.

## Open
- Owner actions: `CLOUD_PUBKEYS`/`MAKTABA_BROADCAST_PUBKEYS` and the edge's kill variables at
  deploy.
