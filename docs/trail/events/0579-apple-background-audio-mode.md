---
schema: 1
kind: pr
pr: 579
title: The Player's background audio mode never shipped
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 055885f19. `INFOPLIST_KEY_UIBackgroundModes` is not a key Xcode generates, so iPhone/iPad builds, TestFlight included, had no `UIBackgroundModes`: PiP was never possible (button disabled by `!pip.isPossible`) and leaving the app cut the sound.
- It is now in `info.properties`, and the watch and live players use `.continuesIfPossible`.
- Tests: MaktabaKit `BackgroundPlayback` checks the plists and policy; UI tests press Home and check playback moves on.

## What we learned
- Check the built app's Info.plist, not the build settings: not every `INFOPLIST_KEY_*` is generated.
- The simulator does not enforce background modes.

## Open
- Device behaviour still to confirm on the next TestFlight build.
