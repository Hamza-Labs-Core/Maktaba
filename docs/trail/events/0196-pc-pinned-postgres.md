---
schema: 1
kind: pr
pr: 196
title: Pinned Postgres per platform, Postgres on Windows
date: 2026-10-21
session: ce7a4c40
source: claude-trail.md
---

## What happened
Pinned Postgres programs per platform (pgbin) and Postgres on Windows were split out of #178
into #196.

## What we learned
- `postgres.exe` needs a restricted, non-admin token: it refuses to run with an admin token.
