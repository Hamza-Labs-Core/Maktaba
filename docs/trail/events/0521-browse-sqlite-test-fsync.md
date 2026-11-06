---
schema: 1
kind: pr
pr: 521
title: TestLibraryTabsSQLite over the unit budget: fsync during setup; embed-cache test compared timings
date: 2026-11-02
session: unknown
source: claude-trail.md
---

## What happened
- CI run 41449508335 (PR #510) failed test-budget: browse TestLibraryTabsSQLite took 1.42 s, and 1.32 s when re-run alone. The 3x400 ms cap is 1.2 s.
- Cause: the shared `testDB` setup (about 45 autocommits, plus about 14 from `groupsEnv`). On a file DB in rollback-journal mode each commit syncs the journal, the db file and the directory. That is nearly free on macOS and costly on the Linux runner. Simulated locally with `PRAGMA fullfsync`: setup took 965 ms of a 990 ms test. The five handler requests take about 25 ms, so the handler was not the problem.
- Fix: `testDB` opens with `_pragma=synchronous(OFF)&_pragma=journal_mode(MEMORY)` in the DSN, so every pooled connection gets them. Same idea as dd1fb356 (tvchangetest). Simulated fsync: 0.99 s to 0.08 s. Every browse test that uses `newEnv` gets faster.
- The same PR fixes a search test that failed integration/compat-prev: `TestEmbedCacheConcurrentSameKeyNoCorruption` compared response bodies byte for byte, including the wall-clock `took_ms`. Both subtests (concurrent, and sequential miss->hit->hit) now decode the bodies, drop `took_ms` and compare the rest (`withoutTiming`). Passed `-race -count=20`.

## What we learned
- SQLite fsync per commit costs almost nothing on macOS and a lot on the Linux runner, so a test can pass locally and break the CI budget. `PRAGMA fullfsync` simulates it locally.
- Put SQLite pragmas in the DSN so every pooled connection gets them.
- Never compare response bodies that carry wall-clock fields byte for byte.

## Keep doing
- Test SQLite databases run with synchronous OFF and an in-memory journal.
