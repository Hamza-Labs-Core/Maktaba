---
schema: 1
kind: decision
pr: 587
title: Epic 51 spec: configurable components
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 04f09fc84. One catalog of about 30 components. Wanted state (absent/installed/enabled plus a hold that survives restarts) in Postgres; actual state per machine with reason codes.
- Disabling stops taking jobs, drains, pauses what is left with no attempt counted, keeps the queue and catches up on enable. Dependents wait instead of being disabled. Packs come out with each server release under its existing signature. Existing installs are seeded from today's switches and checked by update-rehearsal scenario 12. No new paywalls.
- Rejected: `server.toml` as the store, cascade-disable, one installer per bundle, a third-party store.

## What we learned
- The story writers found about 25 README errors against the code (wheels download on first run, ollama is Mac only, the relay agent cannot be gated, live updates use SSE), all fixed before merge. Check a spec's claims against the code.
