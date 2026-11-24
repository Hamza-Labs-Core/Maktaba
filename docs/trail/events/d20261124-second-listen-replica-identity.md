---
schema: 1
kind: incident
title: "From the PostgreSQL 18 upgrade on 2026-11-20, every second_listen job failed to store: a FULL replica identity held an unpublished generated column"
date: 2026-11-24
session: 03bc2883-be15-4702-bb43-935a6a4b9cab
agent: claude-code
prs: [1612]
---

## What happened
- **2026-11-20 08:11:59Z:** the live home's cluster moved from PostgreSQL 16 to 18
  (`postgres-18/maktaba-upgraded-from.json`, mode clone; v0.3.152).
- **08:12:37Z:** the first `second_listen` job failed at its store with
  `42P10: cannot update table "transcript_segments"` and the detail
  `Replica identity must not contain unpublished generated columns`.
  - The failure is terminal (`retryable: false`).
  - By 2026-11-24 13:12Z, on v0.3.161, there were 46,513 failed jobs on 46,513 videos and no
    success since.
- **Cause:** three things together, read from the live database (read only).
  1. The home's `transcript_segments` is `REPLICA IDENTITY FULL`.
     - So are 149 other tables there that have a primary key.
     - No migration in the repo's history sets FULL on a table with a primary key: slot 0147
       and the switchover's fork publication set it only on a table without one.
  2. The table has slot 0016's stored generated `search_tsv`.
  3. The machine's publication (slot 0147) sends no generated column (`pubgencols = n`).
  - PostgreSQL 18 refuses an UPDATE or DELETE on such a table; 16 skipped generated columns in
    that check.
  - INSERTs are not checked, so `transcribe` kept working.
- **Impact:** 46,476 live videos kept Whisper's words. Their subtitles, search index and tags
  never followed (1,517 such jobs ran since 11-20, all from videos stored before the upgrade).
  The failed jobs are terminal, and the relisten sweep only queued `refine.source = skipped`,
  so nothing would have tried them again.
- **Not the cause:** Epic 49 (slots 0231–0236).
  - Its generated columns are on `video_search_docs` and `search_terms`.
  - Both are left out of the copy, and both are DEFAULT.
- **#1612:**
  - Slot 0237 gives every public table at FULL with a primary key and a generated column
    `REPLICA IDENTITY DEFAULT`.
    - It changes the catalog only, with no rewrite, under a 2 s `lock_timeout` with 30 tries.
    - Its Down puts nothing back.
  - The relisten sweep also queues the videos whose last `second_listen` job failed with
    `cannot update table`, 100 a sweep.
  - Tests on PostgreSQL 18:
    - the pipeline test in the replica job;
    - a guard in the copy-tables test;
    - scenario 17 of the update rehearsal, which runs the stage's registered statements on the
      upgraded home.

## What we learned
- **CI could not see it:** the integration tier's Postgres is 16, and no test published
  `transcript_segments` with FULL identity. A behaviour PostgreSQL 18 adds needs a test on 18
  (the replica job's home server, scenario 17), not only on CI's 16.
- **The live home is not CI's schema:** 150 of its tables are FULL, unlike any database the
  migrations make. Check `pg_class.relreplident` and the publications on the home before
  reasoning from the migrations.
- **Ordering the sweep's query was expensive:** the candidate query ordered by
  `created_at … LIMIT` read all 46,000 candidates (9 s). Unordered, it took 21 ms with
  `processing_jobs_subject_history_idx`.

## Keep doing
- Line the first failure up with the home's own state changes (`maktaba-upgraded-from.json`'s
  `at`): the 38 s between them settled the cause before reading any migration.

## Open
- The other 148 FULL tables with a primary key on the home still log every old row in full
  to WAL, and would break the same way if a migration gave one a generated column. CI cannot
  catch that, because its tables are DEFAULT. Putting them all back to DEFAULT is a decision
  for later.
- Where FULL came from on the home is not known. The 2026-10-23 move from the dev database is
  the likely source, but it is unverified.
- Once #1612 is released, check on the home that the stranded videos drain
  (`transcribe.relisten_queued`, `second_listen` done).
