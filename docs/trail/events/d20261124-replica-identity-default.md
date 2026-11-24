---
schema: 1
kind: incident
title: "Every table with a primary key gets it back as its replica identity (slot 0238), after the home's FULL tables broke second_listen on PostgreSQL 18"
date: 2026-11-24
session: 03bc2883-be15-4702-bb43-935a6a4b9cab
agent: claude-code
prs: [1647]
---

## What happened
- The open item of d20261124-second-listen-replica-identity (#1612, slot 0237): the live home
  still had 149 more tables with a primary key at REPLICA IDENTITY FULL.
  - No migration sets that: slot 0147 and the switchover set FULL only on a table without one.
  - FULL writes the whole old row to the WAL with every change.
  - On PostgreSQL 18, a published FULL table that gains a generated column takes no update at
    all.
- This was treated as a technical fix, not an owner decision.
- **#1647, slot 0238:** every public table at FULL with a primary key goes to DEFAULT; tables
  without one keep FULL.
  - It runs as NO TRANSACTION, with a `COMMIT` after each table inside a top-level `DO` block,
    so each `ALTER TABLE`'s ACCESS EXCLUSIVE lock is held alone and briefly.
  - It changes the catalog only, with a 2 s lock_timeout and 30 tries per table.
  - Its Down puts nothing back and is harmless after a partial Up.
- **Tests:**
  - `TestNoCopiedTableWithAPrimaryKeyIsFull`;
  - update rehearsal scenario 17: the seeded 16 home has every table with a primary key at
    FULL, and after the 18 update none is.

## What we learned
- **Committing inside a migration works through goose:** a `DO` block run at top level (a NO
  TRANSACTION file, sent as a simple query by lib/pq, and by asyncpg with no arguments) may
  `COMMIT` between tables.
  - That spreads a migration's per-table locks over separate transactions instead of holding
    them all until the end.
  - The `COMMIT` must sit outside the `BEGIN … EXCEPTION` block that retries the lock: a commit
    inside a subtransaction is refused.
- Checked through `maktaba-api migrate up` on PostgreSQL 18: 246 tables went from FULL to
  DEFAULT in 3.3 s while another session held `videos` for 5 s.

## Open
- After the release that carries slot 0238, check on the home that no table with a primary key
  is FULL (`pg_class.relreplident`).
