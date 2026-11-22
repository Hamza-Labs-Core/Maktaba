---
schema: 1
kind: incident
title: "Xcode 27 (#1486): the Remote's second confirmation lost, and UI jobs that could not say why an app ended"
date: 2026-11-22
session: f531cea2-971f-4c9c-833d-47dc53bdf492
---

## What happened
- #1486 moves every Apple build to Xcode 27 and the 27 simulators (iOS 27.2 on the xcode-27
  image). Its CI run 44674187921 at b25c72db7 failed four Apple UI jobs. Main's macos-26 runs
  fail about one UI job in four.
- **Remote UI tests:** `testRecoveryRestoreAndDiagnostics` failed with "the second confirmation".
  The screen recording shows the first alert closing and no second one. Two fixes failed
  before the cause was logged:
  - ca1215a37 assumed the alert's close cleared step 2 after the button's task set it. It made
    the step synchronous (`RecoveryModel.answer`) and the close a no-op. CI run 44994509246
    failed the same way.
  - ea8261ae1 assumed a presentation dropped while the first alert was still closing, and
    waited for the window to settle. CI run 45001846039 failed the same way.
  - The step-by-step log (scratch branch `diag/remote-confirm-272`, never merged; runs
    45004838549, 45006590015, 45008299877) showed what happens instead. The button runs,
    step 2 is in place at once, and the first alert is already gone from the window when its
    close is reported. SwiftUI on iOS 27.2 then writes the closed alert's `isPresented` false
    again 30 to 60 ms later, and an alert presented again from the same modifier in that time
    never shows. With 600 ms between them it showed; two modifiers sharing one cleared state
    lost step 2 to the late write.
  - The fix, 827f668d2: the confirmations alternate between two alerts by step parity, both
    read `recovery.asking`, and a close writes nothing. It passed four runs of four (run
    45009911826).
- **Player UI tests (ipad 4/6):** every test passed, then the log step found the simulator
  stopped ("device is not booted"), read no line of the app and failed as "it never ran".
- **Player UI tests (iphone 1/6):** `PlayerLibrariesUITests.testAccessibilityAudit` failed with
  "Application is not running". It was on the fifth of its six launches, right after a tap on
  the address field. The app's log just stops, and the job kept nothing saying why.
  The run before (44659284257) failed the same audit with "Timed out while evaluating UI query".
- In ab014808c, `tools/apple/sim-log.py` now keeps, for the Player, Remote and TV jobs:
  - the app's log;
  - the system's lines on the app's processes ending;
  - the host's crash reports of the Maktaba apps.

  A simulator found stopped is reported with CoreSimulator's host lines, and is booted again to
  read its own log.
- **Player UI tests (ipad 6/6):** the iPad search field does not take the keyboard when tapped
  after a submitted search, on iPadOS 27.2 only. Its cause is in its own event
  (d20261122-ipad-search-tab-role).

## What we learned
- On iOS 27.2 SwiftUI writes a closed alert's `isPresented` binding again 30 to 60 ms after
  its button. The same `.alert` modifier presented again in that time shows nothing.
  Consecutive confirmations need separate presentations, and a binding's close should not
  clear state that buttons own.
- Two fixes judged from a guess at the cause each cost a CI round. Logging the state and the
  window's presentations at each step found the cause in one run.
- A UI job that only keeps the app's own log cannot explain "the app is not running". The
  system's termination lines and the crash report are the evidence.

## Keep doing
- Read a failed UI test's screen recording frame by frame (`ffmpeg -ss`) before guessing. It
  showed the missing alert at once.

## Stop doing
- Re-presenting the same `.alert` modifier from its own button.
- Shipping a fix for a CI-only failure before a diagnostic run has shown the cause.

## Open
- The iPhone libraries audit's app exit: its cause is to come from the next run's
  `*-ends.log` and crash reports.
