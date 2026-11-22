---
schema: 1
kind: incident
title: "Story 49.15 opened: Arabic title matches ranked by similarity on a C-locale database"
date: 2026-11-22
session: 7998f2a8-8a3e-41cf-99b3-4cf9a473ddc3
prs: [1503, 1496]
---

## What happened
- #1503 (open) fixes what #1496's event recorded. Search's "In titles" list ordered by
  `similarity(v.title, q)`, 0 for every Arabic title on the bundled server's `--locale=C`
  database, so Arabic matches came newest first.
- Similarity is now taken over the folded title and query with their Arabic letters spelled
  in ASCII, as 49.8's `search_terms.trgm` spells them.
- `TestBM25_ArabicTitlesRankBySimilarity` runs in the replica job, whose PostgreSQL servers
  are `--locale=C`, and is required there. It fails on the old code.

## Open
- The match is still `ILIKE '%q%'` on the title as written: harakat or a differently
  spelled hamza in the query finds no title.
