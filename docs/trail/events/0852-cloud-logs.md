---
schema: 1
kind: pr
pr: 852
title: The cloud's logs, errors and metrics on the home's model, feeding Epic 50
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- Story 53.17. The keeper and the index moved to `shared/logkeeper/go`. Each cloud process keeps its lines on its node and forwards them to W, numbered and acknowledged. W's `maktaba-cloud logs` keeps and indexes every node's lines.
- Cloud lines name a home by `home_h` and carry the request's id. The relay's server ids and slugs, and the agent's slug, are gone from the lines.
- `cloud/internal/obs` serves the series Epic 50.10's rules already used, by route pattern, with labels fixed in `shared/metrics/cloud-series.md`. `alerts-test.yml` checks both rules.
- The service admin has Logs and Errors views (the Errors view joins problem reports by fingerprint). Every runbook's Check step links to a saved search, enforced by the runbooks' lint.

## What we learned
- A server that returns while its background writers still run loses their last writes and fails `t.TempDir` cleanup. Wait for them, and cancel an inner context on every return path, a failed listen included.
- A role test that calls every admin route as the service admin needs a rule for a route that is honestly unavailable (503 `logs_unavailable`), not a blanket "5xx fails".
- promtool's unit tests refuse a 7-day rate over 1-minute samples. Sample the input every 5 minutes for a week-long baseline.

## Open
- W gets the `maktaba-cloud` binary with Epic 50.07: `deploy.sh` ships to A and B only.
- 53.16's symbolication with dSYMs and its rehearsal T07 are still open.
