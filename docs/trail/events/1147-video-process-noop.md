---
schema: 1
kind: pr
pr: 1147
title: "fix(api): video process and reprocess queue real jobs, never a silent success"
date: 2026-11-11
session: bc3a2286-a1ca-4209-96eb-c9132ee7447e
---

## What happened
- Cause: `POST /api/videos/{id}/process` and `/reprocess` answered 200 with no job in every build.
  `MountP6` built `videos.Handler` without `JobQ`, and no type implemented `videos.JobControl`, so
  the handler's nil branch (`"note": "no-op (no enqueuer)"`) always ran and wrote an audit row
  for work that never existed. Found while building Epic 59.13's NAS checks.
- Since: Story 7.5 introduced the routes with an optional `JobControl` meant to be injected; it
  never was. The library scan had the same soft-fail branch until Story 9.6 wired its enqueuer.
- Impact: no app screen calls the routes, but the public API does: OpenAPI's `processVideo` and
  `reprocessVideo`, and the GraphQL mutations of the same names (which run the REST routes in
  process). Any admin or script using them got a success and no processing.
- Fix: #1147 (merged at 509aa08a5) adds `videos.PostgresJobs` over `queues.Enqueue`
  (`processing_jobs_enqueue`): process queues a forced run, reprocess a forced cascade. The router
  wires it. A refused enqueue is 400 (not a video stage) or 409 (stage switched off), no queue is
  503, and the library scan's nil-enqueuer 202 is now 503 too. Admin-only as before. The
  release-shaped test (`access_pg_test.go`, production router on migrated Postgres) asserts the
  `processing_jobs` rows and the member 403s; it fails with the `JobQ` line removed.

## What we learned
- A handler dependency that is optional "for tests" and soft-fails to success hides a missing
  production wiring indefinitely: the access test asserted 200 on the no-op for months.
- The release-shaped router tests are where wiring bugs show: assert the side effect (a row, a
  job), not only the status code.

## Keep doing
- When a route's work goes through an injected interface, make the nil case an error (503) and
  add a production-router test that checks the work happened.

## Stop doing
- Soft-fail branches that answer 2xx with an empty id when a dependency is missing.

## Open
- None for these routes. Other `if h.X == nil` branches under `api/internal/handlers` were
  checked: they are reads that report "unavailable", not mutations claiming success.
