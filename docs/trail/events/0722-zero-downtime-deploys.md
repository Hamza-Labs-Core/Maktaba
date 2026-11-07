---
schema: 1
kind: pr
pr: 722
title: Cloud deploys with no downtime: standby first, expand-only migrations, blue/green by readiness
date: 2026-11-07
session: c8daad15-7bc0-4b14-a10c-8b559dcb37d3
prs: [721]
---

## What happened
- Story 50.08: `deploy/cloud-ha/deploy.sh` and `bin/bluegreen` (a new colour beside the old, a 30 s stability gate, the old one drains, a watch rolls back), an expand-only lint for cloud migrations, relay.yml's pair deploy, and the rehearsal's deploy scenarios (0 failed requests; a bad release stopped before blue was touched).

## What we learned
- Reloading Caddy's config through its admin API ends streamed responses in flight (8 of 1,317 rehearsal segments ended early with `unexpected EOF`). With both colours as health-checked upstreams and the draining colour answering `/readyz` 503, the edge moves new work by itself and nothing is cut: a deploy needs no edge change at all.
- A readiness check that passes once is not enough to switch: a release that breaks 20 s after it starts must be held for a stability window before the old colour is drained, or the rollback is user-visible.
- Go's `flag` stops at the first non-flag argument: `maktaba-cloud migrate up --config x` ignores `--config`; it must come before `up`.
