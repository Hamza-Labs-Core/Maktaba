---
schema: 1
kind: pr
pr: 1491
title: "Keyword search ranks videos by BM25: phrase boost, passages by spelling and form, hybrid"
date: 2026-11-22
session: 7998f2a8-8a3e-41cf-99b3-4cf9a473ddc3
agent: claude-code
prs: [1482, 1493]
---

## What happened
- #1491 merged as 75fd70721 (head 0f5b72149). Epic 49 story 49.7.
  - **Ranking.** Keyword search ranks videos by the documents' two BM25 indexes, exact and
    stemmed, once both are valid. A video's score is twice its exact score plus its
    stemmed score. The library, language and account filters are in each query.
  - **Phrase boost.** For a query of more than one word, the first 100 videos with a line
    saying the words together rank above the scattered ones.
  - **Hybrid** fuses the BM25 videos with the meaning search's by RRF.
  - **Fallback.** Before the indexes are valid, on PostgreSQL 16 or SQLite, search ranks
    the lines as before. While 49.6's backfill runs, it also finds videos with no document
    yet by their lines.
  - **Migration 0234** stores the normalised text as generated columns (`search_text`,
    `stem_text`) and builds the indexes on them. A BM25 `ORDER BY ... LIMIT k` scores each
    returned row again, and over the expression indexes that was 0.6 ms a row.
- **The spelling-variant fix (review).** A video found through a variant (اسلام finding
  إسلام) showed 0 matches and no passage: the passages were read by the words as typed.
  Now the page's lines are folded in Go (equal to `maktaba_normalize`), matched by their
  folded words, and Postgres's stemmer decides only the lines that need word forms or
  have punctuation inside a word (at most 300, within a 60 ms budget). A 6,000-line page
  takes 12-28 ms. A query with search operators keeps the line path's passages.
- **The 150-result cap (an accepted deviation).** The replica job measured the 45,000-document
  p95 at about 53 ms, against 50, with both legs at their top 100: CI's runner is about twice
  as slow as a laptop. The stemmed leg costs more per row, so it now gives its top 50. Keyword
  search shows up to 150 videos (the union of the two lists), not the 500 of
  `rules.search.max_videos`. The meaning search's videos still fuse in for hybrid.
  Locally p95 is about 15 ms.
- **CI.** The first full run had `win-app / Windows app (x86_64)` cancelled, which failed
  `ci-success`. Main was meanwhile red on an unrelated test-budget breach (#1493). After
  #1493 merged, main was merged into the branch and every check passed, including the
  update rehearsal and `TestBM25_Perf45k`.
- **The session handover.** The first lead's provider hit its weekly limit with #1491 at
  green-but-cancelled. A new lead took over from its thread. It left three scratch Postgres
  servers running, which the new lead stopped.

## What we learned
- A per-row cost in BM25's `ORDER BY ... LIMIT k` multiplies by k. Store what the score
  needs, and keep k as small as the page allows.
- Folding and matching in Go, over the page's lines, is far cheaper than a SQL function
  per line (40 µs a line in SQL).
- A cancelled job fails `ci-success` like a failed one. Re-run it, or bring main in, rather
  than waiting for it to pass.

## Open
- Issue #1492: the review's minors (the perf fixture without lines, the untested timeout
  branches, Latin letters ø/ł/æ that fold differently from Postgres).
- The cap of 150 videos, unless a later story makes the stemmed leg cheaper.
