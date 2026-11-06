---
schema: 1
kind: decision
pr: 61
title: Plex sees Maktaba as an emulated HDHomeRun tuner
date: 2026-10-19
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [139, 159, 214]
---

## What happened
Maktaba emulates an HDHomeRun tuner plus XMLTV for Plex, as dizqueTV does: Plex Live TV
integration (#61), discoverability fixes (#139), Add to Plex adds Maktaba as a channel source of the
DVR (#159), and XMLTV guide images and descriptions like dizqueTV (#214).

## What we learned
- Auto-detection by Plex was never solved; Add to Plex adds it as a channel source instead.
