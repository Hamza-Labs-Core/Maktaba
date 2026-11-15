---
schema: 1
kind: pr
pr: 1317
title: "CI fails an Apple UI job whose result bundle ran none, or not all, of its tests"
date: 2026-11-14
session: ff9eaade-bd8f-4224-9087-4fbcea60ab40
agent: claude-code
---

## What happened
- Written after the pull request merged, from its description, commit and diff; the session
  that did the work left no event.
- In CI run 43605618404 (attempt 1), `Player UI tests (ipad 6/6)`, a `-test-iterations 20` loop
  for #1312, ran 1 iteration of 20 and went green. Its log reads `Restarting after unexpected
  exit, crash, or test timeout`, then `Executed 0 tests`, yet xcodebuild printed
  `** TEST EXECUTE SUCCEEDED **`. The xcresult held one `Expected Failure` and no repetitions.
- #1317 merged as 9063eacfe. Each Apple UI job (the 12 Player shards, both shots shards, TV,
  Mac and Remote) writes its `-only-testing` pairs and any `-test-iterations N` to
  `$RESULTS/<name>.selection`. A new step, **Check every selected test ran**
  (`tools/ci/xcresult_ran.py`), reads `xcresulttool get test-results tests` and fails the job when
  nothing ran, when a selected test, class or target has no test case, or when a test ran fewer
  than N times. Swift Testing `Arguments` children count as one run.
- Tests use xcresulttool JSON recorded from CI (a normal shard, the incident, the full 20-run
  rerun, the Mac bundle) plus zero-run and missing-test fixtures, and pin that the selection is
  written from the same array xcodebuild gets.

## What we learned
- Measured before changing anything: across 33 CI runs, 144 of 144 normal (non-looped) Player
  shards finished exactly their selection. Seven of them hit a runner restart, and each time
  the restarted runner resumed the remaining tests. Only the `-test-iterations` loops
  (43605618404, 43599999810) dropped runs. The check covers both paths anyway.
- A green xcodebuild exit is not proof that tests ran; the result bundle is.
- The last launch's summary line after a restart can undercount (it read 32 when 33 ran), so
  the count comes from the bundle, not the log.

## Keep doing
- Measure how often a CI hole was hit across recent runs before sizing the fix.

## Open
- A single-run test whose body is lost to a warm-up stall is still recorded as an `Expected
  Failure` that ran; the bundle can't tell it from a real expected failure, so this check
  doesn't flag it.
