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
- The first wave that ships a plugin is the first release whose Postgres homes build runners, and three gates assumed none:
  - The roles migration must not fail where it may not make or grant a role (no CREATEROLE; a role another database's owner made on a shared Postgres). It notices, and the worker runs those stages in-process (`role_unusable`).
  - A runner that cannot start must judge neither the update's start nor its watch, like a TV slot. The update e2e's fake release has no runner module, and its runners crash looped.
  - A test that "no runner is built" must set an empty bundle; the release's own now has one.
- With 11 runs a side, N16's p95 is about the slowest run. One CI stall failed a stage whose measured steps summed to 15 ms, so a timing-only failure is measured once more.

## Keep doing
- Run the replica, updates and unit jobs' suites (`go test ./...` in cmd/maktaba-server and api) before pushing a wave, not only the plugin jobs.

## Open
- Waves 2-9 and the removal. `display_title` moves with wave 4.
