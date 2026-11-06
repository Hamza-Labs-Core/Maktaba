---
schema: 1
kind: decision
pr: 198
title: One per-user Windows installer, no admin, every download pinned
date: 2026-10-21
session: 295d4839
source: claude-trail.md
prs: [169]
---

## What happened
All Windows state is per user in `%LOCALAPPDATA%\Maktaba` (#169). There is one current-user
NSIS installer with the whole server inside, no admin, and every download sha256-pinned (#198).
