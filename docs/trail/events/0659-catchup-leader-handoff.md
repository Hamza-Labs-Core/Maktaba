---
schema: 1
kind: pr
pr: 659
title: The worker that opens a component's stages queues its catch-up
date: 2026-11-06
session: f0e36dc6-64c3-44f2-980b-fac44d831b70
issue: 646
---

## What happened
- components-live failed on #655 (a docs PR; run 42222286503) at `test_live.py:108`, waiting 60 s for `component_catchup`. The kept job dump had no catch-up job at all.
- Cause: each pass of a home worker's component runner takes the stage-gate leader lock anew. live-1 led transcription off, live-2 led it on. The time a component went off lived only in the off-leader's memory, so live-2 reopened the stages without queuing the catch-up, and live-1 never led again because no change followed. Which worker wins the lock is random, hence the intermittent failure.
- Fix: `_write_home` reads what its own gate write reopened (component_off -> open) and queues the catch-up for it, with `off_since` from the home's status rows. A runner that turned a component off and finds it already reopened drops it, so the catch-up isn't queued twice.
- The components tests keep each local run in `components-logs/<time>-<pid>/` (the newest 20) with a failed test's report, and the dump now carries payloads and the components' rows.

## What we learned
- The workers' logs in the artifact were enough to read the hand-off: `component_stage_gates_written` appears in live-1's log for the off and in live-2's for the on.
- `job_stages.updated_at` is no good as the time a component went off: `sync_graph` rewrites it at every worker start.
- The earlier unexplained local failure fits the same cause.

## Keep doing
- Keep state that a leader acts on in the database, not in one process's memory, when leadership is taken per pass.
