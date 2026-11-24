---
schema: 1
kind: pr
pr: 1561
title: "Search changes the screen once the keyboard has hidden; a hung UI test is cut at 10 minutes; the Mac app's crash reports kept"
date: 2026-11-24
session: 71f9924d-5382-420c-bb6a-d0e85685bd01
agent: claude-code
prs: [1561]
---

## What happened
- This follows #1550 on what it left open.
- **The cap:** the Player UI shards run with xcodebuild's per-test allowance
  (`-test-timeouts-enabled YES`, 600 s). The longest test that passed in the surveyed runs
  took 411 s, including its shard's first launch. Twice on #1550 (run 45264580791, iPhone 2/6)
  a test hung in its tearDown after "Failed to terminate" and held the shard until the
  job's 60 minutes ran out.
- **The logging:** Search taps that changed nothing left no word of whether the action ran.
  The app's log now has:
  - Search's steps: each change asked for, whether it waited for the field, when it runs.
    The words are never logged.
  - Under the UI tests' touch hook: the view each touch landed on, and every gesture
    recognizer's state as it ended.

  A diagnostic PR (#1562, closed unmerged) ran `PlayerSearchUITests` alone on all 12
  iPhone and iPad shards with it.
- **Search's "never went idle" found and fixed:**
  - The diagnostic's first run (45296281858) caught it on iPhone
    (`testDidYouMeanRunsTheCorrectedWordsInArabic`).
  - The log shows Return asking for the search, the field letting go of the keyboard at
    00:45:09.267, and "search runs" at 00:45:09.267, the same millisecond. Two 60 s idle
    waits followed.
  - `LetGoFirst` waits for the field to drop focus, which is when the keyboard starts to
    hide, so the results replaced the screen mid-animation. This is #1419's stall, which its
    Open list had named for Search.
  - #1561 runs the held change on iOS only after `keyboardDidHideNotification` (a new
    `KeyboardWatch`): at once when no keyboard is up, and after 2 s at the latest.
  - The second run with the fix (45302034688): 11 of 12 shards green, with no idle wait and
    no lost press. The one failure was the audit's "Text clipped" below.
- **The audit's "Text clipped", explained:**
  - It flagged AI mode's "27:02" again, at the same frame as on main, `(63, 274.67, 68.33,
    19.67)`. The screenshot shows it whole.
  - The screen recording during the audit shows the app stepping through text sizes (the
    clipping check lays the app out at each size, as `accessibilityAudit`'s comment says).
    At an accessibility size, the List's cells had not yet resized, and the enlarged source
    card was cut off by the one below it.
  - So the check measures mid-relayout on a slow simulator. No fix yet: excusing the check
    would be a skip.
- **The Mac's "Lost connection":** the Mac app's last line was "connect" at 18:33:53. The next
  test's app launched at 18:34:04, so the app ended without a word. The Mac job now keeps the
  Maktaba apps' crash reports from `~/Library/Logs/DiagnosticReports`.
- **"The test runner failed to initialize for UI testing (Timed out waiting for AX loaded
  notification)"** happened once (iPad 1/6 of 45296281858), and in no other job log on hand.

## What we learned
- "Failed to terminate" is older than #1550's console hook. On main before it, iPad 3/6 of
  45189795489 (`testShimmer`, the shard's third test) failed the same way, and its shard went
  on after about 80 s. The two hangs on #1550 were the Cast test as the shard's first test.
  The app was alive: it served its stream proxy, and RunningBoard logged no termination
  request until it was killed.
- MaktabaKit's `WalkthroughModel` timeouts, as far as checked:
  - Both failures ended `.unknown`, which is the timed-out-before-any-signal path.
  - A feeder on the main actor was the first guess, and it does not hold. `ScriptedProbe`
    buffers its whole script, and in Swift 6 mode the feeder's answers are queued before the
    ticker's next job.
  - Local runs with the main actor hogged (0.3 s and 1.2 s slices) and the full suite in
    parallel under CPU load did not reproduce it.

  No change was made to it.

## Open
- Search's lost presses (Filters after Return; a recent search after a row was removed): none
  in 24 diagnostic runs of the suite. The Filters case followed Return, so the keyboard fix
  may cover it. The next failure's app log will say whether the action ran.
- "Failed to terminate": cut at 10 minutes now, but the test still fails. The next
  occurrence's spindump will show where the runner was.
- The audit's "Text clipped" measured mid-relayout (above), the Mac's "Lost connection" (its
  crash report comes with the next one), and `WalkthroughModel`'s 1 s timing.
