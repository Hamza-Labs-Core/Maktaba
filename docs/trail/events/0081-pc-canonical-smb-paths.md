---
schema: 1
kind: decision
pr: 81
title: Paths are canonical smb:// share paths
date: 2026-10-19
session: 89e92223
source: claude-trail.md
prs: [101, 107]
---

## What happened
With the PC and the Mac in one cluster, file paths became canonical `smb://<server>/<share>/...`
paths (#81). #101 made the home convert its own paths, and #107 made that automatic conversion
safe after a post-merge review.
