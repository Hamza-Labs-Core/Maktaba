---
schema: 1
kind: pr
pr: 1412
title: "Apple UI tests: leaving the player and choosing a segment get one more try; the demo player waits out a stall"
date: 2026-11-15
session: b47dc279-403a-4d10-8efc-873b659d9e1a
prs: [1249, 1309, 1402, 1405, 1407]
---

## What happened
- PR #1405's run 43712326838 (iphone 6/6) failed `testTimeUpMidVideoStopsAndIsNotTriedAgain` at "Back leaves the player": the kept app log had the time's-up Back as `touch held 0.18 s at (201, 480), cancelled` and nothing changed. A dropped tap on a step #1309 had not covered.
- `PlayerUITestCase.leavePlayer(by:player:_:reveal:)` taps Back or Close, waits for an element of the screen being left to go, and taps once more if it stayed (`reveal` shows controls that hide by themselves); a second miss fails naming the step. Used by every Player UI test that leaves the player: the time's-up Back, the video player's Close (Video, Picture in Picture) and the live player's Back (three Live tests). The Remote has no player; the TV leaves its player with the Siri Remote's Menu, which is not a touch.
- The three-runs proof (run 43715426951) found three more, none in a changed test:
  - TV `testMenuAlwaysLeavesTheLivePlayer`: the banner race #1402 fixed; the branch predated it.
  - Profile's theme: the tap on Light was `touch held 0.07 s at (416, 808)`, whole and not cancelled, and Light was not chosen (a quick tap the picker ignored, as #1249's switches). `PlayerUITestCase.choose` taps a segment until selected; Profile's theme and Live's Now and Guide use it.
  - `PlayerDemoShots.testPlayer` "it plays on" (playing=true, position 423): not a tap. See below.
- Proof: Player iPhone and iPad each 612 passed and 1 failed (the two above); every changed test 6/6.

## What we learned
- A player that reads `playing=true` with its position frozen can have dropped its only HLS variant. The simulator's system log (in the xcresult's diagnostics) had `video-0.ts` through the app's stream proxy get "received 0 bytes in 2.29815 seconds", "No response for media file in 2s", then "Can not proceed after removing variants" and `fpfs_ranDryTimerProc ... pausing playback`. The proxy passed the request on 3 s after the player sent it; the mock home answered in 0.27 s. AVPlayer gives a segment request about one target duration, which is 2 s for the test stream.
- `Playback.watch` recovers such a stall (a look every 10 s, a reopen after `WatchLogic.stuckAfter`, 15 s, of waiting): up to about 30 s. A test that checks that playback moves must allow that, or it fails before the app's own recovery; `testPlayer` now waits 40 s.
- An XCTest screen recording can end before the test does (29 s of a 46 s test here): check its duration before reading a frozen screen from the absence of frames.
- Filter the simulator's log to `coremedia` and `CFNetwork` task lines, and compare a task's `transaction_duration_ms` with when its upstream task resumed: that placed the delay inside the app, not in the home.

## Keep doing
- Read each red iteration's touch log first: a cancelled or ignored tap gets the re-check and retry with a deterministic signal (the screen left, the segment selected); anything else gets its cause found before a wait changes, and a changed wait cites the app behaviour it waits for.

## Open
- The stream proxy's latency on a loaded device: where the 3 s went (the head's read, the start of the utility-priority task, or the token) is not in the logs. A proxy log line at each request's admit and upstream start would show it next time. The player's budget follows the stream's target duration, so a home's own segment length sets how much latency a device can take; when it is exceeded, the stall costs up to 30 s.
