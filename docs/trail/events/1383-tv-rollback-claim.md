---
schema: 1
kind: pr
pr: 1383
title: "A TV rollback's reverse commit is enqueued once, by whoever claims it first"
date: 2026-11-15
session: 27480173-f46d-46f5-9fa8-bb2b69a8efe1
agent: claude-code
---

## What happened
- media-e2e run 43667287347 (PR #1379, unrelated) failed `TestSwapRollbackEndToEnd` with
  `null value in column "live_gen" of relation "tv_routing"`. The failing SQL was the test's
  stand-in for the `tv_rollback` job (`live_gen = previous_gen`), and it ran twice for one
  rollback. By the second run, `previous_gen` was already NULL.
- Root cause, in `api/internal/tvchange/swap.go`: `requestRollback` wrote the rollback operation
  (uncommitted, no `enqueued_at`), enqueued `tv_rollback`, and only then stamped `enqueued_at`. A
  controller tick in that window enqueued a second reverse commit. In a household, the pipeline's
  real commit (`tvrelease/rollback.py`) would fail the second job on `LookupError` (change not
  live / no rollback point): the rollback itself works, but a failed TV job is left behind.
- #1383 merged as ab35d3b5c. `claimEnqueue` stamps `enqueued_at` in one guarded UPDATE (not
  committed, no stamp within `reaskAfter`) before either path enqueues, and `releaseEnqueue`
  gives the claim back when the enqueue fails. No migration.
- `TestWarmRollbackRacingATickEnqueuesOneReverseCommit` forces the interleaving: its queue hook
  runs `ctl.Tick` inside the API's enqueue. It failed with 2 jobs before the fix and passes with 1
  after. The e2e now asserts one reverse commit per admin rollback, and passed locally on a
  scratch pgvector container (152 s).

## What we learned
- A "stamp after the side effect" de-duplication is a race whenever two goroutines or processes
  can take the same action. Claim first, with a compare-and-set in the database, then act.
- An e2e stub that is stricter than the real job is valuable: the real commit refuses the second
  run quietly, while the stub's NOT NULL error exposed the double enqueue.
- `affected-tests` wants a changed test in the changed file's own package directory. Tests in a
  sibling package (here `handlers/tvchange`) do not count.

## Keep doing
- Reproduce a CI race by forcing the interleaving in a hook (run the competing step inside the
  first one's call), never by looping the test.

## Stop doing
- Matching CI checks by bare job name in merge watchers: reusable workflows report
  `media-e2e / media-e2e`, so a watcher that looked for `media-e2e` never merged a green PR.

## Open
- Nothing for this fix.
