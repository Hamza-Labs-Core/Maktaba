---
schema: 1
kind: pr
pr: 1274
title: "Publishable sources and geo rules (story 50.23)"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1274 merged as b0453eefc, after #1263, so home migration 0228 followed 0227. A published
  channel airs only publishable items: the household's own works, items cleared with a proof
  link, Creative Commons Attribution videos (licence read from YouTube at intake and refresh),
  and sources with a recorded permission. The rule lives in SQL functions (SECURITY DEFINER, as
  plugin parity requires); the Broadcast page shows each channel's coverage for the next 7 days
  and the sources that would add most. Five admin-only routes, covered by the role test.
  Turning the limit off takes a typed confirmation. Per-channel country allow or deny lists plus
  Maktaba's own deny list; the edge refuses a viewer outside and writes main's `blocked geo`
  analytics event once per session.

## What we learned
- `affected-tests` reads main's coverage map: a test that exercises a file but is not linked to
  it in the map does not count. Extend a test the map already links (here
  `test_guide_horizon.py`) instead of adding a new file.
- When a later story (50.28) defines a shared event schema, an earlier branch's own event shape
  must be rewritten to it at merge, or the rollup misreads positional columns.
- Main moved several times while the stack waited; each re-merge conflicted only in CHANGELOGs
  and string tables. Resolve string tables as the other side's file plus only this side's keys
  (no duplicates, no reordering), then check no key from either side is missing.

## Open
- Owner action: the geo deny list and its legal review.
