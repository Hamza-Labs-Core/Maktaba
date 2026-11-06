---
schema: 1
kind: pr
pr: 232
title: VOD on NVIDIA used a nonexistent hwaccel
date: 2026-10-22
session: 454c33cb
source: claude-trail.md
prs: [237, 244]
---

## What happened
VOD transcodes on NVIDIA used `-hwaccel nvenc`, which does not exist. #232 decodes with cuda and
gives NVENC its own preset. #237 made VOD share the GPU with TV without starving the hot
standby, and #244 made VOD playlists list every segment.
