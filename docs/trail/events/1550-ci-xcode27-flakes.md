---
schema: 1
kind: pr
pr: 1550
title: "Main's red CI after Xcode 27: the app's stderr blocked XCTest's queries, a PIN read raced the sheet, Roku's grid wrapped"
date: 2026-11-23
session: 71f9924d-5382-420c-bb6a-d0e85685bd01
agent: claude-code
prs: [1550]
---

## What happened
- Main's CI failed on every push from b6378fa8c to 7fa45b504 (runs 45182333067, 45189795489,
  45199851694, 45238693973), so the scheduled Release session had nothing to tag. A rerun of
  45238693973's failed jobs failed Roku again.
- Every Player UI, Roku and TV UI job on xcode-27 since #1486 was surveyed from the job logs:
  13 main runs on attempt 1, only 5 with all of those jobs green, and 19 failed jobs. The
  failures grouped into the causes below.
- **XCTest's queries waiting on the app's stderr (7 of 12 main Player UI failures):** "Failed
  to get matching snapshots: Timed out while evaluating UI query" (five tests), "Audit failed
  to complete in time", and "Search never went idle". XCTest's spindumps in the result bundles
  (iPhone 1/6 of 45189795489, iPhone 4/6 of 45182333067) show the app's main thread inside
  XCTest's own query (`XCTElementQueryProcessor`). Each attribute read logs through os_log, and
  `_os_log_impl_stream` blocks in `writev` → `spec_write` → `lck_mtx_sleep` for 312 of 325
  samples; each query took 15 to 20 s before one timed out. Xcode launches the app with
  `OS_ACTIVITY_DT_MODE`, which copies every log message to stderr. #1550 adds
  `ConsoleHook`: a Debug build launched with `-maktaba-console-off` points fd 2 at /dev/null
  first thing in `PlayerApp.init`. The unified log, which `tools/apple/sim-log.py` keeps,
  still has every message.
- **PIN pad (3 failures):** `digitsEntered()` read `exists` and then `label`, two queries. The
  right PIN's sixth key closed the sheet between them, and the label's query failed ("No
  matches found for pin.dots"). It now reads one `snapshot()`.
- **Roku T03, then T04:** the Library grid was a `PosterGrid` with the default
  `vertFocusAnimationStyle`, `fixedFocusWrap`. Down on the first page's last row (30), while
  the second page was still loading, wrapped focus to 0, so eleven Downs ended at 30, not 66.
  A real Roku wraps the same way. T04 then failed because it assumes T03 left the channel in
  Arabic. The grid is now `fixedFocus`, and the test waits for the second page at row 30.
- **iPad 1/6 of 45199851694:** all 34 tests passed, then the results upload failed with
  `ENOTFOUND` from the artifact service. That upload continues on error now.

## What we learned
- `OS_ACTIVITY_DT_MODE` is read once, at the process's first log message, and any value turns
  the stderr copy on, "NO" and "0" included. A small os_log program shows it. It cannot be
  cleared through `XCUIApplication.launchEnvironment`, and `OS_ACTIVITY_MODE=disable` also
  keeps the messages out of the unified log.
- XCTest saves a spindump of the app with a "Timed out while evaluating UI query" failure.
  `xcrun xcresulttool export attachments --test-id ...` gets it, with the screen recording and
  the debug descriptions, from the kept `player-ui-tests-*` artifact.
- Each of the twelve failures looked like a different test's bug. Grouped by message and
  stack across every run, two causes explain ten of them.
- A `PosterGrid` or `MarkupGrid` wraps vertically by default; a paged grid wants `fixedFocus`
  or `floatingFocus`.
- `exists` followed by a property read is two queries. Read a snapshot when the element can
  go away in between.

## Keep doing
- Survey every run's failed jobs and group them by message before fixing any one test.
- Pull the spindump out of the result bundle for a UI-query timeout.

## Stop doing
- Treating "Timed out while evaluating UI query" as a slow runner to wait out with longer
  timeouts.

## Open
- Single failures with no cause found yet: the facets' sheet not opening after its tap
  (iPhone 3/6, 45189795489; the tap reached the app), "Failed to terminate" (iPad 3/6,
  45189795489), AI mode's "Text clipped" on a source's time (iPhone 5/6, 45238693973), and
  the Mac's "Lost connection to the application" (45238693973). The Apple TV and Remote UI
  tests do not set the console flag yet.
