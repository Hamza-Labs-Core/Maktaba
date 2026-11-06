---
schema: 1
kind: pr
pr: 470
title: Three flaky tests fixed at the root
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Fixed three flakes at the root.
- Enrich near midnight was the test, not the product. `seconds_to_next_day` floors at 60 s, so `not_before` can be exactly now + 60 s. The test now reads the clock before the run and bounds inclusively. After review it also pins the enrich test's budget clock, so midnight between its two runs gives no fresh allowance.
- Windows Resume was a product bug. `mutex_held` meant "the mutex exists". An exiting agent has released it but keeps the handle until its process is gone (Task Scheduler still counts it running), so `start_task` sent one `/Run` into IgnoreNew. Now "held" means owned (a zero-timeout wait).
- The unit-tier soft cap's run-again reused the first run's `tmp_path`, so any test that `mkdir`s or appends there failed the run-again, and one runner stall failed the test.

## What we learned
- Swapping only `tmp_path` (the first fix) left fixtures built on it (e.g. `_settings` in `tests/models/test_fetch_cli.py`) on the first folder. Found in review. The soft cap now runs a slow test again through `runtestprotocol` (setup, call, teardown, all function-scoped fixtures afresh, as pytest-rerunfailures does) and logs the reports only after judging. A forced re-run of all 274 cluster unit tests passes; before, 66 failed.
- A "> 3x soft cap" failure with no "run again" time means the run-again raised, not that the test was slow twice. The same signature failed `test_linux_nfs_mount_found_through_proc_mounts` on #467: 0.945 s and no run-again time, because the re-run raised.
- Cross-checking Windows Rust from this Mac: rustup's cargo (Homebrew's shadows it), the `x86_64-pc-windows-msvc` target, `CC_x86_64_pc_windows_msvc=clang-cl` from Homebrew LLVM, `AR_…=llvm-lib`, plus stub `assert.h`/`string.h`/`stdlib.h` via `CFLAGS_…="/imsvc <dir>"` so `ring` builds. `cargo check` and `cargo clippy` then work. No linking, no running.
- Seen on this PR's integration run, not caused by it: `TestRowCacheInvalidationPostgres` (`api/internal/handlers/browse`) failed with a scan dropping libB's entry too. Likely a late notice of an earlier write dropping libB after `fill()` saw both rows cached. #471 then made that test wait on its own notice marker.
