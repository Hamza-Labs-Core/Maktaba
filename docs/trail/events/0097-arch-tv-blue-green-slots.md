---
schema: 1
kind: decision
pr: 97
title: Two TV slots, blue and green, with staging and switchover
date: 2026-10-19
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [104, 108]
---

## What happened
Epic 33: two TV slots, blue and green, with hot standby (#97). An update stages the new release on
the standby slot, checks it (#104, staging warm-up and verification), then switches (#108, hot swap
in the front door with rollback).
