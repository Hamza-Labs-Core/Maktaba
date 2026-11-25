---
schema: 1
kind: pr
pr: 1738
title: "The desktop screen tests ran effects on OkHttp's thread, and a screen could lose a change"
date: 2026-11-25
session: 8fc89d92-c76f-4cb4-b0eb-8036123ea989
agent: claude-code
---

## What happened
- `SearchScreenTest.anOlderHomeAndAnAdminsSwitchEachSayWhy` timed out on Windows (issue #1678 item
  11, run 45551600730). The home's answer was in the model; the screen never showed it.
- The deprecated `runComposeUiTest` runs effects unconfined: after an answer that is OkHttp's
  thread, which wrote the screen's state while the test's thread composed. With starved threads 2
  of 370 runs lost the change, and 0 of 800 with `androidx.compose.ui.test.v2`.
- #1738 moves the desktop suite to v2, as the Android suites already were. It closes #1678; its
  leftovers are #1739, #1740 and #1741.

## What we learned
- Print the model beside the screen when a wait fails: they differed, and that ended a wrong
  theory (a refusal's body lost) that fitted the symptom. What it found in the code is real and
  not this: #1741.
- For a failure seen once in 200, a structural check says more than a count: which thread an effect
  runs on was 60 of 60 against 0 of 400.
- A deprecation warning can name the cause: the build had printed this one 72 times.
