---
schema: 1
kind: pr
pr: 1303
title: The integration tier's pytest time measured; a session time line
date: 2026-11-14
session: e544ce0a-14f1-480d-ab5f-e2eacb792032
agent: claude-code
prs: [1295]
---

## What happened
- #1303 merged as 092e740af. Every pipeline pytest session now ends with a `session time:`
  line: collection, and setup/call/teardown summed over the tests
  (`pipeline/tests/conftest.py`, tested in `pipeline/tests/test_conftest_guards.py`). The Makefile comment above
  `test-integration-inner` records the numbers below. The tier's command is unchanged.
- Two fixes were built, measured in CI and dropped: collecting only the test modules that can
  hold an integration test (a static source scan behind a `--integration-files` flag, proven
  loss-free by a whole-tree equality test), and a warm-up collection as its own step before
  the tier.

## What we learned
- Inside the tier, collection was 125s of a 215s pytest session; the tests summed 86s. The
  cost is cold bytecode, not the test count: the tier's pytest is the job's first Python
  run, and it compiles everything the tests import (via the root conftests,
  `maktaba_pipeline` included) on one core while four Go builds use all four. Cold with the
  runner to itself: 14.8s; warm: 3.9s; warm beside one Go build: 9.9s. `uv run` itself:
  under 0.1s.
- Collecting half the modules (2,685 items instead of 5,213) still took 118.8s. A warm-up
  collection before the tier (20s) cut it to 54s, but the tests then overlapped the heaviest
  Go phase and took the time back (summed 194s).
- The tier's wall is the `api` module's go test plus ~1-2s in every run measured (247/299/
  300/240s), so pytest isn't on the critical path. Cutting it saves at most the CPU it hands
  back to Go, and runner noise on the same tests (`api` 240-300s) is larger.
- `--durations=25` can't show this: the 25 slowest tests summed ~30s, and the slowdown
  is spread over 1,100 items and collection.
- Measuring outside the tier (a temporary step, warm, uncontended) pointed the wrong way.
  Only a measurement inside the tier showed where the time goes.

## Keep doing
- Read the `session time:` line before changing anything in a pytest tier: collection vs
  summed test time vs session.
- Before cutting a side that runs in parallel, check which side is the critical path
  (here: tier wall minus `api`'s time).

## Stop doing
- Don't time a tier's pieces only outside it: cold caches and CPU contention are the cost.

## Open
- Shortening the tier means shortening `api`'s go test (132 packages, 240-300s), not
  pytest.
- The pipeline's Postgres parametrisations (`db[postgres]`, ~870 tests) are skipped in the
  tier: `MAKTABA_TEST_PG_URL` reaches only the Go tests (`INTEGRATION_GO_PG_URL`). The job's
  later file-test and untiered steps do set it.
