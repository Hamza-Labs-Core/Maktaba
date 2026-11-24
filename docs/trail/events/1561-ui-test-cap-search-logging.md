---
schema: 1
kind: pr
pr: 1561
title: "A hung Player UI test is cut at 10 minutes; Search's steps and each touch's recognizers in the app's log"
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

  A diagnostic PR (#1562, not to be merged) runs `PlayerSearchUITests` on all 12 iPhone and
  iPad shards with it.

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
- Search's lost presses: the next failure's app log will say whether the action ran.
- "Failed to terminate": cut at 10 minutes now, but the test still fails. The next
  occurrence's spindump will show where the runner was.
- The audit's "Text clipped" on texts drawn in full, the Mac's "Lost connection", and
  `WalkthroughModel`'s 1 s timing.
