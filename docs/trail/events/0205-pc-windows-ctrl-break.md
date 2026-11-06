---
schema: 1
kind: pr
pr: 205
title: Every role stops cleanly on Windows
date: 2026-10-21
session: 295d4839
source: claude-trail.md
prs: [222]
---

## What happened
Story 38.5 made every server role stop cleanly on Windows (#205); #222 held the venv bootstrap
and a setup step while a stop was asked.

## What we learned
- CTRL_BREAK cannot reach a child that has its own console.
- CTRL_BREAK sent to a pid that leads no process group hits the whole console.
