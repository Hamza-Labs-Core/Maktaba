---
schema: 1
kind: decision
pr: 652
title: Epic 55 spec: desktop Player parity
date: 2026-11-05
session: unknown
source: claude-trail.md
prs: [651]
---

## What happened
- Merge 51b149bb0. The Windows and Linux Player still ships with every tag but had stopped at Epic 42.4 (sign-in, a library browser and playback).
- The spec has a 42-row gap inventory against the Android and Apple Players, the plan for sharing `:client`, the JavaFX approach to subtitles and audio tracks, and 10 stories; 55.02 specifies the capabilities-gate switch from #638.
- The scope changed from building to spec only partway through, so the build agents were stopped and their unpushed scaffold was dropped.
- #651 merged first and made #652 conflict in the wiki index and generated wiki files; main was merged in, both rows kept and the wiki regenerated (`--check`: 1389 of 1389 entries).
