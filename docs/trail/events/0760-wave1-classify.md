---
schema: 1
kind: pr
pr: 760
title: Four classify stages run as Maktaba's built-in plugin (Epic 54 wave 1, 54.28)
date: 2026-11-07
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [750]
---

## What happened
- `promo_links`, `clip_groups`, `topic_collections` and `rate_style` moved into `tv.maktaba.classify`. They run in the `light` runner through 36 named statements under `maktaba_fp_classify` (migration 0199). The parity proof drains a seeded channel library both ways and compares rows, queue records and job logs.
- The flip at update is each worker's graph sync: a first-party stage never set by hand becomes `plugin` on Postgres.

## What we learned
- The parity harness found two transport bugs no unit test did:
  - Go streams a large answer chunked, and an HTTP client that reads only `Content-Length` corrupts the next request on a kept-alive connection.
  - An exception frame's `type` key overwrote the frame's own `type`.
- A statement under `SET LOCAL ROLE` cost five round trips (begin, role, origin, statement, commit). A connection set to the role once makes it one.
- Deterministic parity seeding must patch `uuid.uuid4` with random-looking ids. Ids that differ only in their low bits collide wherever code takes a hex prefix (YouTube ids).
- This Mac's load average reached about 160 from other sessions, so local job times say nothing; N16 is judged on CI's runner.

## Open
- Waves 2-9 and the removal. `display_title` moves with wave 4.
