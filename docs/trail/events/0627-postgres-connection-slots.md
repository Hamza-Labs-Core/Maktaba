---
schema: 1
kind: pr
pr: 627
title: Live home hit Postgres 53300: room for every program's pools
date: 2026-11-04
session: unknown
source: claude-trail.md
---

## What happened
- Merge d933a396b. The live home hit Postgres 53300 (connection slots exhausted) three times on 2026-11-03, failing update scheduler ticks and health samples.
- The pools could add up to more than 200 against the bundled Postgres default of 100. The streaming service's uncapped pools opened 28 each and the node's home pool was uncapped; nothing leaked.
- The bundled Postgres now allows 300, streaming pools are capped with a one-minute idle close, the node's home pool is capped at 4, and the pipeline closes idle connections after 60 seconds.

## What we learned
- Add up every program's maximum pool size against Postgres max_connections; uncapped pools exhaust slots without any leak.
