---
schema: 1
kind: pr
pr: 204
title: The Windows app runs its own Postgres
date: 2026-10-21
session: 295d4839
source: claude-trail.md
---

## What happened
Story 38.10: the Windows app runs its own Postgres.

## What we learned
- `postgres.exe` refuses an admin token; it must run with a restricted one.
