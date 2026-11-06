---
schema: 1
kind: pr
pr: 590
title: Android: foldable cover bar, cached pages, readable attention groups
date: 2026-11-03
session: unknown
source: claude-trail.md
---

## What happened
- Merge 3d1fc7b1b. On a Razr Fold's cover screen, the Remote's "Needs attention" tab label wrapped to two lines and ran into the screen edge. Pages also went blank when switching tabs, because each tab's state was thrown away.
- Bar labels are one line at one shared size, inset from the edges, in both Android apps. Pages keep what they showed while they refresh. Attention groups show translated titles with clean detail lines instead of raw grouping keys.
- Screenshot tests cover the Galaxy Fold, Razr Fold (density estimated) and Pixel Fold cover screens at text sizes up to 2.0, in English and Arabic.
