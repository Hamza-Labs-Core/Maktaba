---
schema: 1
kind: incident
title: "Keyword search scored each result again from its whole transcript: 180 ms on the home's long documents"
date: 2026-11-22
session: 7998f2a8-8a3e-41cf-99b3-4cf9a473ddc3
prs: [1507, 1505, 1491]
---

## What happened
- Story 49.9's relevance check (#1505) failed on CI: الله and two other common words took
  52 to 56 ms at p95, on a synthetic library of 3,000 documents. 49.7's perf test had passed
  at 15 ms on 45,000.
- The difference was document length. A BM25 query of #1491 scored each returned row again
  from its whole text: 1.8 ms for a 5,000-word document, the home's average (23.1 M lines
  over 45,000 videos). On 5,000 such documents a search took 180 ms. The perf test's
  documents had 400 words.
- `EXPLAIN ANALYZE` showed the index scan returning its top 100 in 0.6 ms; the rest was the
  `ORDER BY` value worked out again above it.
- pg_textsearch's planner hook replaces that value with the scan's own score
  (`bm25_get_current_score()`). Its source (`src/planner/hooks.c`) shows two conditions:
  the `ORDER BY` must be the operator alone, and it finds its objects by unqualified name
  on the session's `search_path`. The bundle puts the extension in its own `textsearch`
  schema, on no path.
- #1507 sets the app role's `search_path` (clusterRev 6) and reads the score through
  `bm25_get_current_score()`. A search takes 2 ms on the long documents, so both indexes
  give their top 500 again (49.7's 150 was there for the cost).

## What we learned
- A perf test's data must have the production data's shape, document length included, not
  only its row count. Measure where the time goes (`EXPLAIN ANALYZE`) before tuning around
  it.
- An extension in its own schema can silently lose features that depend on name lookup.
  Read its planner hooks before choosing where it lives.
- A table small enough to be read whole gets no index scan. A score that only an index scan
  provides needs a fallback (here: all scores 0, ask with the operator).

## Keep doing
- Build the relevance and perf fixtures from the home's measured shape: words per document,
  vocabulary size, rare-word share.

## Open
- The home's own relevance run (story 49.9).
