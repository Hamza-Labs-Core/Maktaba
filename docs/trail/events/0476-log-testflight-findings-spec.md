---
schema: 1
kind: pr
pr: 476
title: Spec of the TestFlight findings for Epic 48
date: 2026-11-01
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Specced the findings from TestFlight testing.
"No audio" traced to codec-less `audio_tracks` rows, not the Player's audio session (already `.playback`); the fix is #477.
AirPlay and the video page's poster became criteria of stories 48.24 and 48.19; 48.19 gained `maktaba://video/{id}`.
