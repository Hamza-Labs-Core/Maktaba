---
schema: 1
kind: pr
pr: 1374
title: "Broadcast viewers on the home's own Broadcast panel"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1374 merged as 639e7305f. The home's Live TV → Broadcast panel has a Viewers section:
  viewers now, viewer-hours for today, 7 days or this month against the plan's allowance, and on
  the Broadcaster tier the peak, average watch, countries and devices, folded exactly as the
  cloud folds them ("Other" kept, privacy note shown). Admins only, enforced by the home API.
- The home never holds the owner's cloud bearer: home API → relay agent (allowlisted route,
  passes only period, date and tz) → a new cloud home door `GET /v1/broadcast/home/analytics`
  that answers only the credential's own home (another home's id gets 403).

## What we learned
- A new OpenAPI route also needs `generate_wiki.py` run; the wiki job fails otherwise.
- `affected-tests` wants real assertions in tests the map links to each changed file; a role
  test for the route, a mount test through a stub agent, and an integration test on the cloud's
  real routers covered all three files without trailers.
