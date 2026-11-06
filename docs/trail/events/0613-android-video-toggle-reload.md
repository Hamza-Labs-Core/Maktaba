---
schema: 1
kind: pr
pr: 613
title: Android video page: a toggle during a reload is kept
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge e91dab862. `favoriteAndMarkWatchedToggleAndPersistOnReload` failed now and then at its "and off again" step because of a real product bug.
- `VideoPage.load()` wrote back favorite and watched from a read the server answered before a newer toggle's save, so a tap during a reload was undone on screen while the server kept it. The test waited only until the read was asked, not answered, so its click could land in that window.
- `VideoPage` counts toggles, and a read that began before one keeps what was tapped, including Mark watched's playback state. `aToggleWhileThePageIsReadAgainIsKept` holds the read's answer on a latch and reproduces the bug every time without the fix.

## What we learned
- A read that started before a write must not overwrite the write's result on screen. Latch tests that hold the read's answer reproduce this every time.
