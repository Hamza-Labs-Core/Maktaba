---
schema: 1
kind: pr
pr: 276
title: Every update failure says why; Mac app builds again
date: 2026-10-25
session: 9bd8e01c
source: claude-trail.md
prs: [307]
---

## What happened
A failed update gave no reason. #276 made every attempt show its reason and link its logs. It
also fixed Mac notarization, which broke on empty `APPLE_*` variables, and made a machine follow
its home's database by itself after the Mac moved its Postgres without the node's role (#307
finished that).

## What we learned
- Empty `APPLE_*` environment variables break Mac notarization.
