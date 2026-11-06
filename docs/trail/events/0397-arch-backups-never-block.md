---
schema: 1
kind: decision
pr: 397
title: Backups never block an update
date: 2026-10-28
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Backups never block an update (#397): they are taken on the side, complete (vectors kept) and
fast (under 10 minutes). Restores skip publications.
