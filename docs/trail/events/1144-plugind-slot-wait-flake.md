---
schema: 1
kind: pr
pr: 1144
title: "test(plugind): the slot-wait test holds the slots itself, so a loaded runner cannot flake it"
date: 2026-11-10
session: 0d86b380-949d-42d5-84cd-b0828100311c
---

## What happened
- `a_wait_for_a_slot_is_not_the_jobs_time` (plugind `crates/core/tests/host.rs`, added in #848)
  flaked in the `plugins-sandbox` (macOS) and `plugind` jobs on main and on PRs that do not
  touch plugind (#1119 and #1120 recorded it passing on re-run; run 42953163702 failed it again).
- The failure was the test's own precondition, not the property: one unit of guest work was
  timed at 841 ms on a loaded runner, the short job then ran in 349 ms and waited 1742 ms, under
  its 2523 ms limit (three timed units). Its outcome was `Ok`.
- The host has no bug: a job's deadline is `started + timeout`, `started` taken after both slot
  acquires (`engine.rs`, `run`).
- #1144 (merged at 8aa71d7da) changes only the test. The two long jobs hold the plugin's two slots
  until the test cancels them. The test waits on `health().running == 2`, sends the short job,
  and frees the slots only once the short job has waited past its whole limit, measured from its
  arrival. The test asserts `wait_ms > limit` directly. The short job's limit is a wide
  5 units + 1 s.
- Proof: with the old clock put back (deadline from arrival) the test fails (`Timeout`, waited
  2719 ms against 2125 ms). With the fix: 60 isolated runs and 30 whole-suite runs, all 28 cores
  busy, no failure.

## What we learned
- A timing test that sizes every phase from one calibration run flakes when the runner's load
  changes between the calibration and the run. Let the test control the phase it measures
  (here, holding the slots and cancelling) and keep calibration only for the side with a wide
  margin.
- `Host::cancel` plus `health().running` are enough to drive slot occupancy from a test; long
  jobs need their own invocation ids (`req()` names them `inv-{stage}`).
- Locally, Homebrew's `rustc` comes first on PATH and has no `wasm32-wasip2`, so
  `make plugind-guests` fails while `rustup target add` says the target is installed. Build the
  guests with `~/.rustup/toolchains/stable-aarch64-apple-darwin/bin` first on PATH. Without the
  guests, every guest-based host test fails with "is missing".

## Keep doing
- Put the old behaviour back to check a timing test still catches the bug.
- Stress a timing fix both isolated and inside the whole parallel suite, with every core busy.

## Stop doing
- Re-running a timing flake without reading which assertion failed: here it was the precondition.

## Open
- None.
