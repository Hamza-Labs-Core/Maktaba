---
schema: 1
kind: decision
pr: 411
title: Loading placeholders wait 0.4 s; returns are instant
date: 2026-10-28
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [413]
---

## What happened
Loading placeholders wait 0.4 s and match the page (#411). Returning to a page is instant from a
per-user cache, and changes drop stale answers (#413).
