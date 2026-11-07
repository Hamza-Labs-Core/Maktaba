---
schema: 1
kind: pr
pr: 818
title: Update-rehearsal scenario 15 and plugin stages in components-chaos (Epic 54, 54.07 T09, T10, T14)
date: 2026-11-07
session: b17a35c4-3127-455d-bdb2-c21890fa281a
agent: claude-code
prs: [814]
---

## What happened
- Scenario 15: a home with three plugins (A any version, B a range that excludes the next releases, C kept off) takes an update that fails its watch and one that passes; A and B Running, B waiting `incompatible` (never failed) on the releases outside its range and Running after the rollback, C kept off, B's queue kept. The kit builds `maktaba-plugind` and the `hello` guest. components-chaos flips a plugin among its targets; its 400 jobs all finish on the first attempt. Closes #708.

## What we learned
- The rehearsal's pipeline is a stand-in that runs no job, so a plugin's state needed a source on the home: #814's supervisor part.
- The release that fails its watch has an API that crash-loops by design: a check while it runs reads the database (the status row), not the API.
- A plugin's stage has no `job_stages` row until a worker syncs the graph with it; the chaos test enqueues its jobs once the row is there and checks the enqueue's outcome (`inserted`), or a missing stage drains trivially.
- The components workers rewrite the tracked `pipeline/run/runners/areas.json` with local paths; restore it after a run, never commit it.
