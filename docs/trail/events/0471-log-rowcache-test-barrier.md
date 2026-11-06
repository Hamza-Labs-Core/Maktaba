---
schema: 1
kind: pr
pr: 471
title: The row-cache test waits on its own notice marker
date: 2026-11-01
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Flake fixed at the root: `TestRowCacheInvalidationPostgres` now waits on its own `pg_notify` marker before it checks, so a late notice of an earlier write cannot drop an entry after the fill.

## What we learned
- One connection hears notices in commit order, so a marker sent last is heard after every earlier notice.
