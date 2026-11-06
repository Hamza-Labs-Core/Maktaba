---
schema: 1
kind: decision
pr: 178
title: Full nodes keep a live copy of the home's DB
date: 2026-10-22
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [349]
---

## What happened
Every full node keeps a live copy of its home's database and runs on it while the home is away
(Epic 37, #178). The home database uses per-machine client certificates (#349).
