---
schema: 1
kind: correction
pr: 1505
title: "Story 49.9's code merged after its event; four Epic 49 pull requests were stacked to stop conflicting"
date: 2026-11-22
session: 7998f2a8-8a3e-41cf-99b3-4cf9a473ddc3
agent: claude-code
corrects: d20261122-499-relevance-found
prs: [1505, 1506, 1513, 1515, 1517, 1519]
---

## What happened
- #1506, the trail PR for story 49.9, merged before its code PR #1505. Its event calls #1505
  open. #1505 merged later as 098cb218a88db16cd1fc6852e41a3d0cb9db868b and shipped what the
  event describes: the relevance generator, `TestBM25_Relevance`, `maktaba-api
  search-relevance`, the document phrase boost and the و/ف fallback. Story 49.9 stays
  partial: the run on a home's own transcripts and its tuning are still to do.
- After #1505 merged, four open pull requests conflicted with main and then with one another
  as each was updated: #1513 (49.10 follow-up), #1515 (49.11 Apple), #1517 (49.11 Android,
  Android TV, desktop) and #1519 (49.11 big screen, Roku). All four edit the same lines:
  `CHANGELOG.md`, `CHANGELOG.ar.md`, the Epic 49 spec's As built section, and the 49.9 and
  49.11 rows of `docs/wiki/epic-status.md`.
- They were stacked. Main went into #1513, #1513's branch into #1515, #1515's into #1517 and
  #1517's into #1519, each with `tools/dev/merge-main.sh <ref>`, which keeps both sides of the
  changelogs. Each conflict was resolved in favour of the combined state. The spec now has one
  49.11 section with a part per client. The status page has one 49.11 row, which reaches Done
  in #1519, the last of the stack. The counts were recomputed with `epic_status.py`.

## What we learned
- Pull requests that each touch the shared status lines conflict in turn whenever one of
  them merges. Stacking them, or moving the shared-line edits into the last one, lets them
  merge in order with no further conflicts.
- A trail PR can merge before its code PR. When that happens, the next trail PR records the
  merge commit and corrects the event.

## Keep doing
- Merge a stack in order. Each pull request's base already holds the one before it.
