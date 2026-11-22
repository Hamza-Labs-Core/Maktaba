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
  The screen recording shows the first alert closing and no second one.
  - The first fix, ca1215a37, assumed the alert's close cleared the second step after the
    button's task set it. It made the button's step synchronous (`RecoveryModel.answer`) and
    the close a no-op. CI run 44994509246 failed the same way: the state was right, and still
    no alert showed.
  - The cause is the presentation. On iOS 27.2 SwiftUI drops an alert presented while the
    previous alert is still closing. In ea8261ae1 the view shows its own `shown`, set only once
    the window has no alert left and no presentation opening or closing.
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
- On iOS 27.2 a SwiftUI alert presented while another is still closing never shows, even
  when its `isPresented` reads true throughout. A chain of confirmations must wait for the
  previous presentation to be gone.
- A fix judged from a guess at ordering, not from the evidence, cost a CI round. The
  recording showed the state; the presentation was the question.
- A UI job that only keeps the app's own log cannot explain "the app is not running". The
  system's termination lines and the crash report are the evidence.

## Keep doing
- Read a failed UI test's screen recording frame by frame (`ffmpeg -ss`) before guessing. It
  showed the missing alert at once.

## Stop doing
- Presenting the next alert or sheet from the previous one's button without waiting for
  the previous one to close.

## Open
- The iPhone libraries audit's app exit: its cause is to come from the next run's
  `*-ends.log` and crash reports.
