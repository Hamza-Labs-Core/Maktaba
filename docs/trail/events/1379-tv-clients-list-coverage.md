---
schema: 1
kind: pr
pr: 1379
title: "The tv-clients list check also requires every p58 router test"
date: 2026-11-15
session: 43ed6e31-0740-4d65-b657-61c1a1d3ba1b
agent: claude-code
---

## What happened
- #1379 merged as f633cc22a.
- **The concern raised:** `tools/ci/tv-clients-tests.txt` had no rows for #1313, #1331 or #1287, so
  the tv-clients rehearsal might skip their tests.
- **The audit:**
  - The rehearsal runs only Go tests in `api/internal/router` and
    `streaming/internal/capability`.
  - #1313, #1331, #1350, #1357, #1287, #1354, #1356 and #1360 added none. Their tests are
    client tests (brs-engine, Robolectric, XCTest, Playwright) that run in their own jobs.
  - Every Epic 58 router test on main was already listed.
  - So no rows were missing.
- **The real gap:** `TestTVClientsListNamesEveryTest` only compared the list with what the
  job's `-run` pattern matched. An Epic 58 router test named outside that pattern would never
  run in the rehearsal, and the list and the pattern would still agree.
- **The fix:** the test now also requires every `Test` in `api/internal/router/p58_*_test.go`
  to match the pattern. A planted stray test failed it, naming the test. The list's header now
  says client tests run elsewhere.
- **media-e2e:** `TestSwapRollbackEndToEnd` failed once on this pull request with
  `null value in column "live_gen" of relation "tv_routing"`. The pull request touches nothing
  there, and the job passed on main's two runs before it. One rerun passed, and a separate pull
  request fixes the race behind it.

## What we learned
- A list check held only to a test-name pattern can't see a test the pattern misses. Pin the
  scope by file (`p58_*_test.go`) as well as by name.
- A "missing rows" report can come from client tests that were never in the rehearsal's scope.
  Check what the job actually runs before adding rows.
- GitHub refuses to rerun one job while its run is still going. Wait for the run to finish,
  then use `gh run rerun --failed`, which also reruns `ci-success`.

## Keep doing
- When an unrelated job fails, compare it with main's runs before rerunning, and open a fix for
  any real bug the failure exposes.
