---
schema: 1
kind: pr
pr: 178
title: Every full node keeps a live copy of the home's database (Epic 37)
date: 2026-10-20
session: ce7a4c40
source: claude-trail.md
prs: [145, 157, 234, 240]
---

## What happened
Requirement (session 798feb8d): when a node cannot reach its home, the site keeps working,
shows a notice and disables actions instead of going blank. That became #145. Its review set
outage rules: no sign-in during an outage, videos play when their path is reachable, jobs
pause. Playback with the home's database down became #157.

Then the design changed: every joined node keeps the home's whole database, read-only, so it
can work read-only and sign-in works offline, all in one feature branch merged once. #145 and
#157 closed into #178, which also built switching the home (Plex and Cloudflare included).
#178 merged 2026-10-22 after five reviews; #240 took two low findings.

## What we learned
- Logical replication, not physical: Postgres cannot stream physically from an arm64 Mac to an
  x86 PC, and a physical copy would carry every secret and session id.
- One slot per node; the copy pauses while the home's schema is ahead; secrets and other
  machines' rows stay out.
- A node's replication role could read every table through `test_decoding`. Now only
  `pgoutput` ships and foreign slots are dropped.
- The freeze was not a fence: lib/pq's `BEGIN READ WRITE` beats
  `default_transaction_read_only`.
- A stream-key race refused the first playback after a start (#234).
- Decisions: standby homes only; late writes are kept aside with an apply; the supervisor runs
  the cloud relay; credentials move through the encrypted handoff, never the replica.
