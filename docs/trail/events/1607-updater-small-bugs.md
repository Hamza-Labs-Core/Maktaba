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
  - The release that switches writes into the guard and the pending marker how long its release may run (`max_run_seconds`: `pendingCap + WatchBound + 45 min`); a marker without one gets `layout.MaxGuardRun` (24 h).
  - The launcher counts the running time, and `ExtendGuard` and `ExtendPending` stop once it is used up.
- **P10:**
  - An order the app never stages ends `app_no_answer` at the run's install-by (else 3 h, never under 30 min).
  - The app's failed start with its order is logged once per error.
  - The Linux runtime order, which stages nothing, goes on without the app after 10 minutes.
  - `app_no_answer` has its words on web, Apple and Android.

- **After its review (Kimi K3.1 Pro: 2 majors, 2 minors):** `app_no_answer` counts no attempt (three windows without the app had blocked the release); a release is confirmed watermark first, marker next, database record last; the runtime's fallback never overwrites a late answer or a withdrawn order.
- **After the second review:** the cap counted by the wall clock rolled a good update back, and held the home, at the first look after a 5 h power-off. The launcher counts running time instead, across its starts (`guardclock.go`); a rollback under way has its own.
- **After the third review:** the new clock kept the time used when the launcher could not go back (the kept copy gone, a full disk), so every start was stopped at its first look, for good (180 starts in 11 s in the repro). The time starts again, twice as long each time.

## What we learned
- **A new counter has to answer what the old reset did.** The old code forgot its version on a failed rollback, which gave another 15 minutes; the new key survived it. Look for every place that cleared the thing a change replaces.
- **A limit on a release is a limit on its running time.** By the wall clock, a power cut, a sleep or a clock set forward uses it up, and a clock set back gives it away.
- **The updater rig's clock moves a minute on every read.** A deadline check made at every tick of `await` would fire in other order tests on a slow CI runner. The check looks once a minute of wall time (`AnswerCheckEvery`, -1 in tests).
- **The `affected-tests` gate counts tests per changed source file, not per package.**
  - New words in `WebWords.kt`, `StatusWords.swift` and `registry.ts` each need a test in their own client.
  - A change to `shared/go/layout` needs a test there, even when the launcher's tests cover it.

## Keep doing
- Put the fix on the confirming side too: the new release's code always runs. The command's side only helps once the old release has the fix.
