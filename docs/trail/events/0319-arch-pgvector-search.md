---
schema: 1
kind: decision
pr: 319
title: Search moved from Chroma to Postgres pgvector
date: 2026-10-26
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Semantic search moved from Chroma to Postgres pgvector (#319, Epic 43, 2026-10-26), the more
efficient option.

## What we learned
- Chroma broke whenever a worker was killed mid-write.
