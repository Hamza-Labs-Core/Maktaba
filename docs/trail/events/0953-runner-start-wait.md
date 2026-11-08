---
schema: 1
kind: pr
pr: 953
title: The first jobs after an update ran in the worker beside a runner that was starting
date: 2026-11-08
session: fee973b6-18bb-4f5c-ada5-07d7a4cd4e95
---

## What happened
- Update rehearsal 16.8 failed on #946 (run 42435097027): after the update, `job_stages.impl` for `plex_groups` was `plugin`, but both jobs queued next ran `inproc`. The shared runner groups start on demand and exit when idle, and an update restarts every process. The worker asked the supervisor for `plugin-runner/service` and chose in-process at once; the runner served seconds later. `hello()` also cached "no answer" for 15 s.
- `native.serving()` now waits for a runner it asked for (`await_runner`), up to 30 s after the ask. There is no wait without a supervisor, after a refused ask, or for a group that already failed to come up. "No answer" is cached for 1 s. `test_native_runner_start.py` reproduces the race.

## What we learned
- `impl = 'plugin'` is written at graph sync (`engine/sync.py:_flip_first_party`), not when a runner registers. It means a job may go to the runner; `native.choose()` decides per job.
- An on-demand runner is always down right after an update, so any check of "new jobs run in the runner" depends on the worker waiting for the start.

## Keep doing
- Read the home log in the rehearsal's `rehearsal-logs-*` artifact. The supervisor's "wanted: starting it" line and the jobs' `job_dispatched` times showed the race to the millisecond.

## Open
- `serving_group("fp")` (the fingerprint runner) still does not wait for its runner. A job whose fp runner is down takes its input fingerprint in-process, which gives the same hash.
