---
schema: 1
kind: pr
pr: 1000
title: "The Remote's diagnostics UI tests tap a Home row once it stands still"
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [959]
---

## What happened
- Main's Remote UI tests failed (run 42461751776, 4b021ae5a): `testRecoveryRestoreAndDiagnostics` waited 15 s for the recovery parts. The hierarchy kept at the failure showed the app still on Home: `open()` swiped the list and tapped the recovery row while the list still coasted, and the tap only stopped it.
- `open()` waits until the row's frame reads the same twice 0.3 s apart, taps, and checks the row left the screen; a swallowed tap gets one more tap on the still row, and a second miss fails naming the row.
- Proven with a temporary commit running the suite three times in one job (`-test-iterations 3`): 21 of 21 passed; the commit was reverted before the merge.

## What we learned
- A tap on a scrolling SwiftUI list stops the scroll and selects nothing. After a swipe, wait for the target to stand still, then check that the tap did what it should.
- `-test-iterations N` in a temporary commit proves a UI fix in one CI job; raise the job's `timeout-minutes` with it.
- An xcresult keeps the app's hierarchy at the failure (`xcresulttool export attachments --test-id`): it tells a wrong screen from a slow one.

## Keep doing
- Read the failure's hierarchy before calling a UI failure flaky.
