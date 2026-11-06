---
schema: 1
kind: pr
pr: 364
title: TestSwitchTheHome was a real bug
date: 2026-10-27
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
`TestSwitchTheHome` failing was a real product bug, fixed in #364: a machine back after a home
switch could strand a table's first copy, so its subscription never finished.

## What we learned
- A flaky-looking test can be a real bug; read the log before calling it a flake.
