---
schema: 1
kind: incident
title: "Story 49.8's first suggestions scanned every title and speaker; the perf fixture had none"
date: 2026-11-22
session: 7998f2a8-8a3e-41cf-99b3-4cf9a473ddc3
prs: [1497]
---

## What happened
- Story 49.8 (#1497, open) adds suggestions as you type, did-you-mean, facets and sort.
  Its first version passed a 45,000-document p95 test at 6.5 ms. The independent review
  (GLM-5.3) pointed out that the fixture had no titles and no speakers.
- With a title on every video and 1 to 3 speakers each, suggest measured 120 ms for ال and
  165 ms for الشي. The speaker and title lookups scanned every row, and the word completion
  summed every word with the prefix.
- The fix makes every lookup stop early:
  - a speakers dictionary (`search_speakers`), kept by the `keyword_index` stage;
  - titles read in index order with `ORDER BY ... USING OPERATOR(pg_catalog.~<~)`, which a
    `text_pattern_ops` btree serves;
  - words read most frequent first.

  p95 is under 2 ms.
- The same review found that `sort=matches` counted lines uncapped, the query the epic
  replaced. It now has the line path's cap.
- The re-review found that a library merge left documents under the deleted library. The
  next rebuild of a moved video then failed on the dictionaries' foreign key. The merge now
  drops them, and the stage rebuilds the moved videos.
- Found on the way: pg_trgm finds no trigram in Arabic on the bundled server's `--locale=C`
  database (trail PR #1496, story 49.15).

## What we learned
- A perf fixture must hold every table the timed path reads, at the home's scale. An empty
  table makes a scan look free.
- A `text_pattern_ops` index serves `LIKE 'p%'`, but an `ORDER BY` on the same expression
  uses it only with `USING ~<~`. Without that it is a bitmap scan and a sort of every match.
- An aggregate kept per library (a dictionary) must follow every way a row changes
  library: a merge, not only an edit.

## Keep doing
- An independent review of each story, with the code and not the conclusion, before CI.
