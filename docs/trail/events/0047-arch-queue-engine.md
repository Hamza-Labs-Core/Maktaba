---
schema: 1
kind: decision
pr: 47
title: Change-driven multi-level queue engine
date: 2026-10-17
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
The change-driven multi-level queue engine (#47). Everything is a queue job, one item at a time,
run in parallel by workers.
