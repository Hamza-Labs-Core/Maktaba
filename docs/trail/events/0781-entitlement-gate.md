---
schema: 1
kind: pr
pr: 781
title: Paid plugins wait for their entitlement (Epic 54, story 54.19 §5, §6)
date: 2026-11-07
session: 28c0b780-1dd6-4ba0-b27f-a3382e1c1cd1
prs: [735, 743, 753, 733]
---

## What happened
- With the home link (#753) and the chain of trust (#733) on main: the store's signed entitlements route, `pluginsig.CheckEntitlement`, the supervisor's check route, the API's daily refresh into `plugin_entitlements` (migration 0204; #773 took 0201 and #789 took 0202 first, and #778 holds 0203), the `entitlement_missing` gate in both runners, and the purchases list on the Store account page.

## What we learned
- #743's entitlement format (one signature, no kind) predated 54.14's envelope, which already had a kind and a key use for entitlements. Merging Phase 2 retired it. When two phases build toward one format, check the other phase's constants (`KindEntitlement`, `UseEntitlement`) before writing your own.
- Only the supervisor holds the verified store keys (its state dir, anti-rollback state). The API holds the home store key that signs store requests. So the API fetches and the supervisor checks, over the loopback routes the package install already uses, and the gates read only `valid_until`.
- A coroutine that never awaits a real loop can be driven with `coro.send(None)`. The pipeline's unit tier refuses sockets, and every asyncio event loop opens a self-pipe socket.
- 54.14's store README already named the secret `STORE_ENTITLEMENT_KEY`. Search the other phase's docs for a name before choosing one.

## Open
- Buy and "Link this home to buy" wait for the home's Browse (54.18). The refresh route takes `after_buy` for it.
- The `/paid/*` CDN path and moving a purchase to another home (54.19 §6).
