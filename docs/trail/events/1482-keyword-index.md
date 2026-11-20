---
schema: 1
kind: pr
pr: 1482
title: "The keyword_index stage and its backfill; BM25 indexes built after the start"
date: 2026-11-19
session: 753035df-3990-44b1-9dff-f305bbe9c3cd
agent: claude-code
prs: [1478]
---

## What happened
- #1482 merged as 00d588d0e. Epic 49 story 49.6, and issue #1479.
  - **The `keyword_index` stage** is a core stage of the always-on component `core.search`,
    on any machine's CPU, after `second_listen`. It builds a video's document from three
    sources and upserts `video_search_docs`, with SQLite's FTS5 row in the same transaction:
    - the lines of the first active transcript;
    - the named speakers of those lines;
    - the shown title, with the file name.
  - **Migration 0233** adds `doc_version` and `transcript_revision`, and seeds the stage's
    `job_stages` row.
  - **The library plan**, every half hour, queues missing or stale documents, one job per
    video at priority 900. A version bump goes through it.
  - **The API** queues the stage when a title is set or a speaker is renamed or merged.
  - **Issue #1479:** the BM25 indexes are no longer built in `migrate up`. The home API's
    `api:search_index` loop builds them after the start: no time limit, under an advisory
    lock, and again every hour. `maktaba-api search-indexes` builds them in the
    foreground.
- **Review.** GLM-5.3 found one major: on SQLite, the row and its FTS5 row were separate
  autocommits, and a retry found the row current and never wrote the FTS5 row. It is fixed
  and tested. The minors are issue #1483. The first review ran out of turns; a narrower
  brief that named the files got through.
- **CI found four things that local runs had not:**
  - the graph's stages must be seeded in `job_stages` by a migration
    (`test_seed_matches_graph`);
  - the website's `dist-manifest.json` must list a new help anchor;
  - mypy rejects patching a module attribute that is not exported;
  - a router test counted every job a title PATCH leaves, and the PATCH now queues one.

## What we learned
- A new core stage needs four things beyond its graph entry: a `job_stages` seed in a
  migration, a component in the catalog (only `core.*` components may be always on), the
  generated files (`make catalog-sync`, `graph_golden.py --write` after `make plugins-sync`,
  the plugin entries golden), and a `dist-manifest.json` rebuild when its help changes.
  Commit `260c99965` (`content_screen`) is a full template.
- An index build has no business in a migration on a table that grows. A build cut short
  leaves an invalid index, and a bounded retry never finishes.
- CI runs on pull requests into main only. A PR stacked on another branch shows a clean
  diff, but its CI waits until it is retargeted.
- Local Postgres tests that print times fail in a non-UTC time zone (`TestP57LiveTVPG`,
  parental). CI runs in UTC.

## Open
- Issue #1483: review minors (the SQLite FTS5 delete by an unindexed column, the loop on a
  SQLite API, the speakers hook's Postgres-only query, the spec row's priority).
