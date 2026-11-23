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
  still has every message. Xcode keeps what the app wrote in the result bundle
  (`StandardOutputAndStandardError-com.hamzalabs.maktaba.player.txt`, via `xcresulttool
  export diagnostics`). Before the hook, iPhone 1/6 of 45189795489 had 143 MB, 1,124,567
  lines, nearly all XCTest's own ("Got attributes, transferring to dictionary for return",
  "(no error) for label using XC_kAXXCAttributeLabel"). With the hook, iPhone 2/6 of
  45264580791 had 7.9 KB: only the `objc` lines written before `main`. Player UI test times
  on #1550's first run against main's 45199851694 and 45189795489: median 34.1 s against 40.5
  and 41.0 s, p90 74 against 89 and 96 s, p99 130 against 161 and 207 s. #1550's runs had no
  query, idle or audit timeout.
- **PIN pad (3 failures):** `digitsEntered()` read `exists` and then `label`, two queries. The
  right PIN's sixth key closed the sheet between them, and the label's query failed ("No
  matches found for pin.dots"). It now reads one `snapshot()`.
- **Roku T03, then T04:** the Library grid was a `PosterGrid` with the default
  `vertFocusAnimationStyle`, `fixedFocusWrap`. Down on the first page's last row (30), while
  the second page was still loading, wrapped focus to 0, so eleven Downs ended at 30, not 66.
  A real Roku wraps the same way. T04 then failed because it assumes T03 left the channel in
  Arabic. The grid is now `fixedFocus`, and the test waits for the second page at row 30.
- **#1550's own runs.** 45255897736 (9b54b8c23): Roku, TV, Remote and Mac green; 12 of 14
  Player UI shards green. 45264580791 (8584a08d7): 13 of 14 Player UI shards green on attempt 1.
  The Apple apps job hit MaktabaKit's `WalkthroughModel` timing and passed on attempt 2;
  iPhone 2/6 hung on "Failed to terminate" twice and passed on attempt 3. Every failure was in
  a class under Open. None of the fixed causes came back: no PIN race, no query, idle or audit
  timeout, and no Roku grid failure. Before, Roku's T03 had failed 3 of main's last 4 runs.
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

- Two runs of #1550 are not enough to call main reliably green. The causes fixed here were
  10 of the 12 Player UI failures on main and every Roku one. What is left fails about one
  Player UI shard in ten, each a class under Open.

## Keep doing
- Survey every run's failed jobs and group them by message before fixing any one test.
- Pull the spindump out of the result bundle for a UI-query timeout.

## Stop doing
- Treating "Timed out while evaluating UI query" as a slow runner to wait out with longer
  timeouts.

## Open
- **Search's lost presses**, the most frequent cause left, seen 3 times: Filters (iPhone 3/6 of
  45189795489) and a recent search (`testRecentSearchesRunAgainAndAreForgotten`, iPhone 6/6 of
  45255897736). The touch reaches the app (`touch held 0.07 s` at the button), the screen
  recording shows the button's pressed look stuck on, and the action never runs: the query is
  not set and no sheet opens. Both buttons are `.borderless` in a `List`, tapped just after
  the list changed (results replacing the spinner; a row removed). The mechanism is not known.
  A Debug log of the button actions would say whether the action ran.
- **"Failed to terminate"** after a test (iPad 3/6 of 45189795489, `testShimmer`; iPhone 2/6 of
  45264580791, `PlayerCastUITests.testCastThenBackgroundThenResumeInArabic`). In the second,
  the app exited on SIGTERM 2.5 minutes after XCTest asked, and the runner then hung in its
  next tearDown until the job's 60-minute timeout cancelled it. That happened twice on #1550,
  on the same test, which is also the shard's first. The app was not hung: it served its
  stream proxy for 1.5 minutes after the request, and RunningBoard logged no termination
  request until it was killed. A diagnostic PR (#1553, closed unmerged; run 45285918070) ran
  the test alone 3 times on each of 6 fresh iPhone simulators. It passed 9 of 9 with
  `ConsoleHook` and 9 of 9 without, so the hook does not reliably cause it. With the hook the
  test hung 2 times in 12 runs, and it passed in every main run surveyed.
- **The accessibility audit's "Text clipped"** on texts drawn in full: the Arabic Refine bar
  (`testDidYouMeanRunsTheCorrectedWordsInArabic`, iPhone 2/6 of 45255897736; its screenshot
  shows both labels whole), and AI mode's source times (iPhone 5/6 of 45238693973).
- The Mac's "Lost connection to the application" (`PlayerMacDemoShotsUITests.testLibrary`,
  45238693973; it passed on rerun).
- MaktabaKit's `WalkthroughModel` tests time out on the 1 s `quick` timing when 798 Swift
  Testing tests share the main actor (Apple apps job of 45264580791; 1 of 17 earlier runs).
- The Apple TV and Remote UI tests do not set the console flag yet.
