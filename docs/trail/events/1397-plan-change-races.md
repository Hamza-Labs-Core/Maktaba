---
schema: 1
kind: pr
pr: 1397
title: "Broadcast: invites and open access can't outlive a plan change; staging watch reports"
date: 2026-11-15
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1397 merged as 2b69a3d29. Creating an invite, switching a channel to open access
  (`SetChannelAccess`) and publishing a channel as open (`Publish`) checked the plan before
  their transaction, so one racing a plan change could leave a live invite or an open channel
  after the plan lost broadcasting (#1393). Each now locks the home's row inside its
  transaction and re-reads the plan; the plan change updates that row before revoking, so they
  serialise. `SetChannelAccess` used to lock the channel before the home, the reverse of the
  plan change's order, which could deadlock; it now takes the home first.
- `TestInviteCreateRacingAPlanChange` and `TestOpenAccessRacingAPlanChange` force the
  interleaving on Postgres (one transaction holds the plan change, the test confirms the other
  waits on the lock); without the fix they return 201/204 and leave access live.
- The webhook pushes each home to the edge once per event. `server.env = "staging"` alone
  accepts https://staging-watch.maktaba.dev for watch-page reports (CORS, origin check,
  Turnstile hostnames); production refuses it.

## What we learned
- A plan check made before a transaction is a time-of-check race; re-check under the same row
  lock the revoking side takes, and keep one lock order across paths.
- Deterministic race tests (hold one transaction, assert the other waits in `pg_locks`, then
  commit) prove the fix without timing flakes.

## Open
- Owner actions for staging reports: `server.env = "staging"` on the staging cloud, and the
  staging edge's `API_ORIGIN` pointing at it.
