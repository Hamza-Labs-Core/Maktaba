---
schema: 1
kind: pr
pr: 1074
title: A stage's impl asked while unchanged is kept through the update's flip (rehearsal 16.7)
date: 2026-11-09
session: 939686fd-f22b-4c85-8a00-2972b85eeabb
prs: [1068]
---

## What happened
- Rehearsal 16.7 failed once, on the run for PR 1068 (job 129029686234): `job 2 ran plugin on "0.3.111", want inproc`. A rerun passed.
- The home log showed `PUT /api/queues/tv_stage/impl` answering 200 in 0 ms. That is the path for a stage already at the asked impl. 0.3.111's pipeline worker had just restarted and had not synced its graph, so tv_stage was still at its seeded `inproc` and the request changed nothing. `impl_changed_at` stayed empty.
- About 0.4 s later the worker's graph sync (`_flip_first_party`) moved every first-party stage with no `impl_changed_at` to `plugin`. Job 2 was claimed after that. `native.choose` reads `job_stages.impl` fresh for each job, so the job ran in the `light` runner.
- This was a product race. The dispatcher was right: no cache, and the impl is not fixed at enqueue time. A request for the current impl just was not recorded as a person's choice, so the next sync overrode it. On green runs the sync came first, the PUT made a real flip, and `impl_changed_at` was set.
- Fix: the API and the CLI now record a request for the impl a stage already has as set by hand. If a sync moved the stage between the read and that record, the API flips it back. New PG tests, one for the API and one for the CLI, each fail without the fix. Rehearsal 16.7's PUT still runs on 0.3.111's binary, which has the bug, so the scenario waits for that release's own flip to `plugin` before switching, then checks that the stage is `inproc` and marked as set by hand.
- The update run that ended `done (error {"held":0,"failed":0,"needs_person":0})` is expected. The driver writes the rollout's tally into the run's `error` column on every finish, including a clean one.

## What we learned
- If a request for the current value answers 200 without recording it, a later automatic default can silently undo that choice. A control that automation also writes must record a person's choice even when the value does not change.
- A rehearsal step on the previous release runs that release's code. A product fix on main does not reach it, so the scenario also has to wait for a state that release makes observable.

