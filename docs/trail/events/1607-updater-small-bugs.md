---
schema: 1
kind: pr
pr: 1607
title: "A command-line switch confirms itself, the guard's deadline has a cap, and an app that never answers ends the order"
date: 2026-11-24
session: 1d933885-c614-466d-9dec-f738a7b6a527
agent: claude-code
prs: [1601]
---

## What happened
These are research item 3's small bugs (`docs/research/update-process.md`, #1601).
- **P4:**
  - `maktaba-server update` on a layout install switched with the run id `cli`.
  - `confirmPending` confirms only a switch with no run id, so the launcher went back after 15 minutes.
  - Now the command switches with no run id, and `confirmsItself` also accepts `cli`. An older command's switch therefore holds once the new release runs.
- **P5:**
  - The release that switches writes `latest` into the guard and the pending marker: `pendingCap + WatchBound + 45 min`.
  - `ExtendGuard` and `ExtendPending` stop there.
  - The launcher never waits past it, nor past `layout.MaxGuardSpan` (a day) for a marker without one.
- **P10:**
  - An order the app never stages ends `app_no_answer` at the run's install-by (else 3 h, never under 30 min).
  - The app's failed start with its order is logged once per error.
  - The Linux runtime order, which stages nothing, goes on without the app after 10 minutes.
  - `app_no_answer` has its words on web, Apple and Android.

## What we learned
- **The updater rig's clock moves a minute on every read.** A deadline check made at every tick of `await` would fire in other order tests on a slow CI runner. The check looks once a minute of wall time (`AnswerCheckEvery`, -1 in tests).
- **The `affected-tests` gate counts tests per changed source file, not per package.**
  - New words in `WebWords.kt`, `StatusWords.swift` and `registry.ts` each need a test in their own client.
  - A change to `shared/go/layout` needs a test there, even when the launcher's tests cover it.

## Keep doing
- Put the fix on the confirming side too: the new release's code always runs. The command's side only helps once the old release has the fix.
