---
schema: 1
kind: pr
pr: 1527
title: "The parental rehearsal proves it catches a missing filter on AI mode"
date: 2026-11-22
session: 7998f2a8-8a3e-41cf-99b3-4cf9a473ddc3
agent: claude-code
prs: [1527, 1525]
---

## What happened
- #1525's re-review noted a gap in `TestParentalBypassCatchesAMissingFilter`, which proves
  the rehearsal sees a leak when one route's filter is lifted. It covered `POST /api/search`
  but not `POST /api/search/ai`.
- #1527 adds the AI route. With its filter lifted, the child asks as an unrestricted viewer,
  and the rehearsal names a leak on `POST /api/search/ai`. The probe's scan, of the sources
  and of what `echoModel` read, is shown to work. It passed locally on Postgres 18
  (`-tags parental_bypass`).

## Keep doing
- When a route gets a rehearsal probe, add it to the missing-filter test as well, so the
  probe is shown able to fail.
