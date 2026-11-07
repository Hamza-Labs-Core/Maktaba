---
schema: 1
kind: pr
pr: 902
title: A test passed only when an earlier run had left its Postgres role behind
date: 2026-11-07
session: 848393e1-ea22-4a25-a90f-b930df232381
---

## What happened
- `test_a_first_party_stage_runs_in_process_unless_its_runner_serves_here` (`tests/engine/test_run.py`) needs the role `maktaba_fp_toy` to be usable. Postgres roles are cluster-wide, and only `tests/plugins` makes that one, and it runs after `tests/engine`. On a fresh cluster the full suite failed there; on a reused one it passed.
- The test is about the runner serving the stage, so it now stubs `role_usable`. `test_native_choose` still covers the role check. Merged as b8e737b25.

## What we learned
- A scratch Postgres reused across runs hides order bugs: anything cluster-wide (roles) carries over. Dropping the role reproduced it at once.
- CI's `unit` job runs only the affected tests, so a whole-suite order bug can sit on main unseen.

## Keep doing
- Run a wave's full pipeline suite on a freshly initialised cluster (UTF8: `initdb -E UTF8`; a default SQL_ASCII cluster fails the Unicode tests).

## Open
- Nothing.
