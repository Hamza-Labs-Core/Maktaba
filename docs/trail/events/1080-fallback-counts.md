---
schema: 1
kind: pr
pr: 1080
title: The in-process fallbacks are counted per machine and shown to admins (story 54.43)
date: 2026-11-09
session: afbdedd7-9eb7-4572-a79a-251f2ae4ea96
prs: [1078]
---

## What happened
- Story 54.43, the counting behind 54.36 §4 precondition (e). Slot 0218 makes `stage_fallbacks` (machine, stage, reason, count, first and latest time; Postgres and SQLite). `SchemaRev` is 218 in its three places.
- The worker keeps parity's in-memory `FALLBACKS` unchanged. It also keeps a second count (`native._UNWRITTEN`) that it adds to the table at each `WorkerAgent.refresh` and when it stops. A failed write keeps its counts for the next refresh and never raises.
- Two new reasons, counted toward the table only: `fingerprint_inproc` (the fp runner did not answer) and `sqlite` (`choose` now names it; every first-party stage on SQLite).
- `GET /api/queues/fallbacks[?stage=]` is admin only, with home placement (it reads `cluster_nodes`). The Queue detail page of a built-in plugin's stage shows **Ran in the worker instead**, in English and Arabic.
- CI found two gates that a new table or module meets:
  - `affected-tests` wants each changed module's own test files touched, so tests in a new file did not count. The tests moved into `test_native_choose.py`, `test_node_runtime.py` and `test_run.py`. A `No-Test-Change` trailer covers the SchemaRev bumps and the placement entry, which existing tests check;
  - integration's `TestEveryTableIsDecidedForTheCopy`: a new table is copied to joined machines unless excluded, and must be listed in `copyReviewed` (`api/internal/cluster/copy_tables_pg_test.go`).
- Merged as b8c6985ff on head 326eea84e with all 108 checks green. The merge call answered "Base branch was modified" while the merge went through: read the PR state after a failed merge call before retrying.

## What we learned
- A new table must be decided for the joined machines' copy: add it to `copyReviewed`, or exclude it in `cluster_copy_excluded()` if it holds a secret or one machine's own state. Run `go test ./internal/cluster/` with `MAKTABA_TEST_PG_URL` before pushing a migration.
- Put a module's tests in the files the `affected-tests` gate maps to it (run `go run . gate -C ../.. --range BASE...HEAD` from `tools/affected-tests`), not in a new file.
- A scratch Postgres for the PG tests runs from the app's bundled binaries (`initdb`, `pg_ctl -o "-p PORT -k ''"`): the scratchpad path is too long for a Unix socket, and port 55432 is taken by another server on this Mac.

## Open
- 54.36 §4 (e) needs a full release with no fallback counted on the live home, read from `GET /api/queues/fallbacks` after the release that ships this lands there.
