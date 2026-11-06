---
schema: 1
kind: pr
pr: 539
title: PlayerWatchTests clock stuck: a previous test left the Player playing
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [575]
---

## What happened
- #575, folded into #539. `PlayerWatchTests` "the time moves" timed out. A UI test left the Player playing (XCUITest does not close the app at the end of a test), so its non-mixable `.playback` session kept the runner's own AVPlayer from starting sound (HALC `_StartIO` error 35), and AVPlayer's clock never moved.
- #539's per-method sharding exposed it by putting the play-from-start test right before.
- Fix: `PlayerUITestCase.tearDown` closes the app, and `PlayerWatchTests.setUp` fails at once if a Player is still running. Not a product bug.

## What we learned
- XCUITest does not close the app after a test; a non-mixable audio session left running blocks the next test's playback.
