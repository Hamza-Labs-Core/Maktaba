---
schema: 1
kind: decision
pr: 78
title: Nodes reach Plex through the home and never hold its token
date: 2026-10-19
session: 89e92223
source: claude-trail.md
prs: [73]
---

## What happened
Nodes run the Plex stages through the home's extension relay (#78) and never hold the Plex
token; secrets live with the home (`home_secrets`, #73).

## Keep doing
- Keep the Plex token on the home only; nodes go through the home.
