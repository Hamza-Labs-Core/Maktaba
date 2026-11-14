---
schema: 1
kind: pr
pr: 1304
title: MaktabaKit stream-ready waits keep their deadline on an injected clock
date: 2026-11-14
session: 61966c72-9802-46d7-a567-342d9664981f
agent: claude-code
prs: [1240]
---

## What happened
- #1304 merged as efa512bdc. `WaitingForAStream.neverReadyIsNotReady()` had failed #1240's
  `apple-apps` job (head 773206674, no Apple changes): 1 wait recorded where `[0.5s, 1s, 2s]`
  was expected.
- `waitUntilPlayable` took an injected `sleep` (the tests record each wait and return at once),
  but it also kept a real `ContinuousClock` deadline beside the summed waits. The tests never
  slept, so the attempt count depended on how fast the stub round trips ran; on a loaded CI Mac
  5 real seconds passed after the first wait.
- The waits and the deadline now use one `clock: some Swift.Clock<Duration> = ContinuousClock()`.
  A wait that would end past the deadline is not started. The tests use a `TestClock` whose
  `sleep` moves `now` on at once, and a new test stretches each wait 2x to show the deadline is
  kept on the given clock. The production callers (Player, Live, tvOS) pass no clock.
- `swift test` in MaktabaKit: 652 tests, 3.8 s (4.0 s before; the suite never slept for real).

## What we learned
- MaktabaKit defines its own `Clock` class: the standard protocol has to be written
  `Swift.Clock` there, in the sources and in the tests.
- A test that injects the sleep but not the time source is still a wall-clock test. The brief
  said the tests slept 3.5 s and 30 s for real; the suite's own timings (0.015 s) showed they
  did not, and pointed at the deadline instead.
- Android's `waitUntilPlayable` keeps its deadline as the sum of its `delay`s (no wall clock),
  and the 59.05 `transcode_busy` retry test runs on Robolectric's idle clock: neither races.

## Keep doing
- Read a suite's per-test timings before accepting that it is slow: they confirm or refute the
  premise in one run.

## Stop doing
- Injecting a sleep while the loop still reads the real clock: inject one clock for both.

## Open
- Android counts only its waits toward the 30 s, not the fetch time; on a slow home it can wait
  longer than 30 s in all. Not a test race; a behaviour choice left as is.
