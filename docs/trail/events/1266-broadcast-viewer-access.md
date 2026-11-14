---
schema: 1
kind: pr
pr: 1266
title: "Viewer access: invites, open channels, limits and tokens (story 50.26)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1266 merged as d9992d671. Each published channel is invite only (default) or open; open
  needs a verified publisher, the Broadcaster add-on and `broadcast.open_access.enabled` (off).
  Only the home's owner or an admin manages invites (link plus `MK-XXXX` TV code, devices and
  days capped) and viewer limits, on the home's Broadcast access page or the cloud's Who can
  watch page. Viewers need no account: a 30-day pass per device from an invite, a 12-hour open
  session after Turnstile or app attestation, a 10-minute viewer token per play. Plan limits
  stop new viewers only. Cloud migration `00500021`; edge `ViewerAccess` Durable Object.
- Staff can list a home's invites (link and code hidden) and revoke one with fresh MFA, audited.

## What we learned
- A withdrawn invite could come back to life once its pass outlived it; the pass now carries
  the invite's expiry (`iexp`) and is capped by it. #1279 reuses that cap for the redirect and
  for TV pairing.
- The T08 load test (1,000 sessions, 10 minutes of beats) takes about 61 s on a normal runner
  and went over 120 s on a slow one; its timeout is now 300 s. A long load test needs a budget
  sized for the slowest runner, not the median.
- The launcher's `tv-web` test (tizen reload reopens the saved home) failed twice on #1266 while
  #1279, a superset of it on the same main, passed; one rerun passed. Watch for it recurring.

## Open
- Owner actions: `TURNSTILE_SECRET`/`TURNSTILE_SITE_KEY`, `ATTEST_API`, the rate-limit
  namespaces; `broadcast.open_access.enabled` stays off until the legal items hold.
