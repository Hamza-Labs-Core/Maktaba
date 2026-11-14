---
schema: 1
kind: pr
pr: 1271
title: "The queue pulse's comments say it is admin-only"
date: 2026-11-14
session: 43ed6e31-0740-4d65-b657-61c1a1d3ba1b
agent: claude-code
---

## What happened
- #1271 merged as 5ed9b546b, comments only. An automated security review on #1268 read
  `Pulse.Stuck`'s comment ("StuckCount is shown to everyone") and the non-admin branch beside it
  as a leak of `workers.stalled` (worker ids, hosts, node names), `host` load and the busiest
  process names to non-admins. The Epic 58 lead confirmed it from the handler alone and
  delegated a fix; the child found every queues route behind `adminOnly` since 5509c4c3e
  (`TestEveryRouteIsAdminOnly`, the parity test, and its own role test all gave 403 before
  `Pulse` runs) and stopped without a PR. The comments in Go and TypeScript now say the whole
  pulse is admin-only; the in-handler check stays, labelled as a second guard.

## What we learned
- A role finding read from a handler is not confirmed until the route's mount is read too:
  middleware on the group (`adminOnly`, `RequireAdmin`) decides who reaches the handler, and a
  stale in-handler branch can look like the only guard.
- A child that finds a brief's premise false and stops with evidence saved a placeholder PR (a
  web view for a payload no client can receive).

## Keep doing
- Fix a comment that misleads a reviewer the day it misleads one; it will mislead the next.
