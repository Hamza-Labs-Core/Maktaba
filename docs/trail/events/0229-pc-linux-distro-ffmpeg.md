---
schema: 1
kind: decision
pr: 229
title: Linux uses the distribution's ffmpeg
date: 2026-10-21
session: 454c33cb
source: claude-trail.md
prs: [220]
---

## What happened
The pinned Linux ffmpeg builds were `--enable-nonfree`, so not redistributable. Decided: Linux
bundles no ffmpeg and the packages depend on the distribution's ffmpeg (#229). The approved
third-party mirror (#220) was closed unpublished.
