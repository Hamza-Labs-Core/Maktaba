---
schema: 1
kind: pr
pr: 346
title: apt stalls were a slow Azure mirror
date: 2026-10-26
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
CI's apt stalls were a 50 KB/s Azure mirror. Fixed with a package cache and a fallback that really
reads Ubuntu's own archive (#346).
