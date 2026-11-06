---
schema: 1
kind: pr
pr: 582
title: Live guide tests open on the guide; settings reset on test launch
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge f4527625. PlayerLiveUITests guide flake (seen on #579): the tap on the Now | Guide switch was dropped on a saturated runner (the recording shows the thumb lifting and settling back on Now; the hierarchy has Now selected), so the guide never opened. Not reproduced locally in about 80 taps under load, so the cause of the drop is unproven.
- The guide tests now launch on the guide (`-maktaba-live guide`), and `testNowGuideSwitch` checks the switch on the spot.
- An iPad Up next failure was a settings leak: the Profile test left autoplay off on the simulator. The app now applies `-maktaba-settings-reset` at start on every fresh test launch.
- Up next now shows when the next episode is found after the video ends (`Playback.nextFound`).

## What we learned
- Settings written by one UI test persist on the simulator and leak into later tests; reset them on every test launch.
