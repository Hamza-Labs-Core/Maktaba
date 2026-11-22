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
  The screen recording shows the first alert closing and no second one. The alert's button
  started a task that put the second confirmation in place, and the alert's close
  (`isPresented` set to false) called `cancel()`. On iOS 27.2 the close came after the task, so
  it cleared the second confirmation. Fixed in ca1215a37: `RecoveryModel.answer` puts the next
  confirmation in place within the button's action, and the alert's close changes nothing
  (Cancel still forgets the action). A unit test covers the step order.
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
  after a submitted search, on iPadOS 27.2 only. Its cause is a separate event.

## What we learned
- On iOS 27.2 a SwiftUI alert's `isPresented` setter can run after a task started by the
  alert's button. State that a button sets for the next presentation must be set
  synchronously in the button's action. The close must not clear it.
- A UI job that only keeps the app's own log cannot explain "the app is not running". The
  system's termination lines and the crash report are the evidence.

## Keep doing
- Read a failed UI test's screen recording frame by frame (`ffmpeg -ss`) before guessing. It
  showed the missing alert at once.

## Stop doing
- Clearing presentation state from an alert's or sheet's `isPresented` setter when a button
  of that same presentation sets the next state.

## Open
- The iPhone libraries audit's app exit: its cause is to come from the next run's
  `*-ends.log` and crash reports.
- The iPad search field's refocus on iPadOS 27.2.
