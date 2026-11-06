---
schema: 1
kind: pr
pr: 570
title: The Postgres reconciler commits each unit; its leader lock is a session lock
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
prs: [561]
---

## What happened
- Merge b07e485d. The Postgres reconciler commits each unit separately, as SQLite has since #561 (one enqueue, one legacy repair, one `propagated_at` mark). Before, one transaction held the whole pass and the leader lock (`pg_try_advisory_xact_lock`), so jobs it queued could not be claimed until it committed.
- The leader lock is now a session lock (`engine/leader.py`: `pg_try_advisory_lock` on a pool connection used for nothing else, released in `finally`, freed by the server if the connection dies). The key is unchanged, so old (transaction lock) and new workers still exclude each other during an update.
- The queue-health round had the same shape and uses the same lock. Backfill commits each row's class, new job and parking together.

## What we learned
- asyncpg: a connection the server ends while in use may not return its pool slot, and `pool.close()` hangs; `leader()` terminates it on the way out.
- Keeping the same advisory-lock key lets transaction-lock and session-lock workers exclude each other across an update.
