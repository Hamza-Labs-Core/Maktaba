---
schema: 1
kind: pr
pr: 1419
title: "The sign-in screens go idle: the keyboard let go of before Connect, no endless spinner by the code"
date: 2026-11-15
session: acaaa0e1-142c-46ed-be0c-558c2dbc2d55
agent: claude-code
---

## What happened
- #1419 merged as b6e482787. It answers the open item in #1418 (the event for #1417): why
  the Player app never went idle on the sign-in screen in run 43724539259, attempt 1,
  iphone 2/6.
- The first 60 s idle timeout came before the "With a code" tap was sent. It was the wait
  right after `picker.connect` swapped the home picker for the sign-in screen. From then on,
  every idle wait in that app process timed out, including on the password form.
- The xcresult screen recording has no frame between 17 s and 77 s: the screen was still,
  so nothing visible was animating. At the swap, the address field still held the keyboard.
  `HomePickerView` is `.disabled(session.connecting)` and is then replaced. The keyboard's
  hide animation was cut off and never reported done.
- Of about 226 recent Player UI jobs, only one other job hit this:
  `PlayerSearchUITests.testATitleOpensItsPage`, with the same pattern (Return in a focused
  field, then a screen change).
- Fix: the picker's address field gives up focus (`@FocusState`) before connecting, whether
  by typed address, Return or scanned code. `SignInView.signIn()` sets `focus = nil` first.
- The wait for approval had an endless spinner for the code's whole life. The recording
  shows 20 fps of redraws for that stretch, which wastes battery on a device. Its countdown
  already ticks each second, so it now shows a still hourglass. With Reduce Motion, no step
  spins (`ConnectProgress.indicator(reduceMotion:)`).
- Tests: the `onlyShortWaitsSpin` unit test. In the UI test, `tapWhenIdle` checks that the
  "With a code" tap after Connect, and the "Username & password" tap while the code shows,
  each take under 30 s. A healthy tap takes 1–2 s; a non-idle app takes 60 s or more.
  The PR's checks were 35 pass, 33 skipped.

## What we learned
- "App animations complete notification not received" with a still recording means a stuck
  animation, not a visible one. Check the recording's frame timestamps (it records only on
  change) before hunting for spinners.
- The retry went idle in under a second with the same spinner on screen. XCUITest's idle
  check isn't blocked by a SwiftUI `ProgressView`, but a device still pays to redraw it.
- Disabling or removing a focused text field while the keyboard is up can leave UIKit's
  animation tracking stuck for the rest of the process.

## Keep doing
- Let go of focus before an action disables or replaces the form that holds it.
- Count how often a CI failure signature appears across recent jobs before calling it
  either a flake or a regression.

## Open
- `PlayerSearchUITests.testATitleOpensItsPage` had the same stall once (job 132841983346).
  The search field submits, then the screen changes with the field still focused. It isn't
  changed in #1419.
