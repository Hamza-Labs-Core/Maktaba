---
schema: 1
kind: pr
pr: 366
title: A full node plays videos it can read
date: 2026-10-27
session: f43fad60
source: claude-trail.md
---

## What happened
The PC node played no local video because the API's check ignored the node's inferred path map
`/Volumes/Originals/` to `O:/`. #366 fixed it and shipped in v0.3.33.
