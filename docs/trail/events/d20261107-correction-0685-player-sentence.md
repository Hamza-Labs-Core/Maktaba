---
schema: 1
kind: correction
title: Correction to 0685 — the Apple player already has its own sentence when streaming is down
date: 2026-11-07
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [685]
corrects: 0685-epic53-strip-rehearsal
---

## What happened
- Event 0685 (#685) lists as open "The Apple player's own error sentence when streaming is down (the strip says it for now)."
- That was already built. `apps/apple/Player/Watch/WatchScreen.swift` shows `StripWords.player` (`player.unavailable`) above the error when `StatusStripRules.playbackDown` is true. It shipped in a225243d5 (stories 53.06 and 53.07), the same change 0685 records.

## What we learned
- Before carrying an open item forward, look for it in the code: `git grep -n "player.unavailable"` settles this one.

## Open
- None: the item in 0685 is closed.
