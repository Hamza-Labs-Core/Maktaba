---
schema: 1
kind: pr
pr: 1312
title: "A Profile page's row tap gets one retry when the system cancelled it (#639)"
date: 2026-11-14
session: c14edd54-169f-4fdd-b154-46ee3e9bc595
agent: claude-code
---

## What happened
- #1312 merged as ed1e6d719. It closes the last flake path found in
  `PlayerProfileUITests.testPlaybackSettingsAreKept` after #1249's `flip(_:to:_:)`.
- The findings of the closed #1289, the first fix attempt for this flake:
  - Root cause of the switch flake, from #1267's iphone 6/6 xcresult (run 43577371468): a whole
    quick tap at the centre of a still switch, with no touch cancelled. The switch ignored it.
  - #1289's row-edge fallback (run 43587125878, 12 shards × `-test-iterations 20`): 240 of 240.
    The first switch tap was ignored in 14 runs (12 iPhone, 2 iPad), about 6%.
  - Main's `flip` after #1249 (run 43599999810, the same loop): 236 of 237, no failure at the
    switch. The one failure (iphone 5/6, iteration 18) was the tap on the `profile.playback` row
    before it. The system cancelled that touch (#639's backboardd race, named by the harness's
    lost-tap prefix). The runner crashed after it, so iterations 19 and 20 never ran.
- `PlayerUITestCase.open(_:shows:_:)` taps a row and waits for its page. It asks the app's
  `TouchHook` for its cancelled-touch count before and after the tap, the same question
  `record(_:)` asks on a failure. It taps once more only when the page did not open and that
  count rose. The retry runs inside an activity named `Retrying a cancelled tap: …`. A tap that
  landed is never repeated, and a missing answer means no retry. The Profile tests open History,
  Sessions, Favorites, Playlists and Playback with it. `PlayerShellTests` covers the decision
  (`retriesTap`).
- Loop on #1312's branch (run 43605618404, the test × 20 on the 12 Player UI shards): 240 of 240.
  The retry activity fired 0 times, counted from each xcresult with `xcresulttool get
  test-results activities`. One touch was cancelled, on the sign-in picker before connect, and
  the harness's field handling tapped it again. The loop commit was dropped before merge.

## What we learned
- With `-test-iterations`, a runner restart drops the remaining iterations. The restarted runner
  logs `Executed 0 tests`, and the job still ends green. The first attempt of ipad 6/6 spent 289 s
  in the warm-up (the picker showed Retry, no shell drawn), recorded only expected failures, never
  ran the test body, and lost 19 iterations. A loop's evidence is counted from each xcresult's
  repetitions (`passedTests`, "N test runs"), never from the job's conclusion.
- The two #639 flake kinds need different detection. An ignored switch tap leaves no trace (no
  touch cancelled), so `flip` reads the value. A cancelled touch is counted by `TouchHook`, so
  `open` reads the count. Neither retry repeats a tap that may have landed.

## Keep doing
- Prove a flake fix with a temporary `-only-testing … -test-iterations 20` loop on the 12 Player
  UI shards, count the repetitions and any retry activities from the downloaded xcresults, then
  drop the loop commit before merge.

## Stop doing
- Reading a looped shard's green check as 20 passes: rerun the job when its xcresult holds fewer
  repetitions.

## Open
- The retry has not fired in CI yet (0 in 240). Its first firing shows as a `Retrying a cancelled
  tap` activity in a Player UI xcresult.
