---
schema: 1
kind: pr
pr: 597
title: README and user guide brought up to date with main
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 107f01d29. A check of whether the README and user guide covered everything found they did not. Against main, the merged PRs and the CHANGELOG, the README still described Chroma, Go 1.23, only the Capacitor and Tauri wrappers and 25 epics. It had no way to get the apps, no TestFlight or Play internal testing, no live TV, watermark, Needs attention, updates, machines, Plex or consent, and no roadmap or domains.
- The in-app help (English and Arabic) did not say how to get any app, put TV approval in the wrong place, left out the Player's sections and the TV apps' Live TV, and was wrong about Android permissions and background playback. All fixed.
- #596 merged while the PR was open, so the docs name its "Approve a TV or device" button, banner and notification. The wiki index got rows for Epics 26-54. Merged after two merges of main (#596, #599).

## What we learned
- The wiki JSON was not regenerated: its generator read a missing May snapshot and would have overwritten newer entries (the generator was reworked in #602).
