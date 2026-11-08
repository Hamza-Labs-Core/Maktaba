---
schema: 1
kind: pr
pr: 1002
title: A process waiting on the venv showed the waiting text over the install's progress
date: 2026-11-08
session: 848393e1-ea22-4a25-a90f-b930df232381
---

## What happened
- `TestAppBundleRunnersWaitForTheVenv` (from #973) failed CI on #996, a test-only Python change. Three runners showed `venv_waiting` where the test expected `venv_building`.
- The cause was in the product. `sharedStep.prepare` read the step's last report under its lock and showed it after unlocking. A broadcast from the running step could reach the new waiter in that gap, and the waiter's first report then landed on top of it. On the Mac app's first start after an update, a runner could keep showing "Waiting for the transcription tools" while the venv installed.
- The first report is now made under the lock. A new test forces the interleaving: the step broadcasts as soon as the waiter registers, and the waiter's first report is slow. It failed every time before the fix.

## What we learned
- A status a waiter shows on arrival must be ordered with the broadcasts it can receive. Read the state and show it in the same critical section, or a newer status can be overwritten by an older one.
- A flaky integration test that asserts a status is often a real ordering bug. Reproduce it deterministically (slow down one side at the gap) before calling it noise.

## Keep doing
- When an unrelated PR's check fails, find the cause and fix it in its own PR with a test. Don't merge past a red check, and don't just re-run until it's green.

## Open
- `TestRestoreAskedPutsTheBackupBackBeforeAnyRole` fails on the owner's Mac (asides empty) with or without this change, and passes in CI. Something about that machine's environment, not yet looked into.
