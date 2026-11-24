---
schema: 1
kind: pr
pr: 1670
title: "A restore that cannot finish always has a safe way out"
date: 2026-11-24
session: 1d933885-c614-466d-9dec-f738a7b6a527
agent: claude-code
prs: [1605]
---

## What happened
- **Taken out of #1605 after its review (six majors), and designed again from one rule:** the record says what state the data is in (untouched, replaced, gone, not known), written before each step that changes it, and each way out (try again, another backup, give it up, a newer release) is allowed or refused by that alone.
- **"Untouched" is proven by the database's own identity** (the cluster, the database's oid, its public schema's oid): every release's restore makes another database or another public schema.
- **One lock** over asking, giving up and the start's own try; the record before the switch; one set aside for a restore; no backup removed until it is done (`backups/restore-hold`).
- **36 real-Postgres cases:** three kinds of Postgres, each stage a restore fails at, each way out.

## What we learned
- **A flag about the data is only as good as the oldest release that may touch it.** A record saying "untouched" is wrong as soon as an older release, which keeps no track, tries in between. Find something in the data itself that the action cannot leave unchanged, and compare that.
- **psql `--single-transaction` commits at the end of its input, wherever that is.** A server killed in the middle of a restore committed a part of the dump (shipped; proven by a test). The commit belongs in the script, as its last line.
- **`pg_restore` may end well on a truncated table** (PostgreSQL 18's). Its COPY's end is missing, and psql takes what follows for rows, the COMMIT too: the restore then said done with nothing committed.
- **A killed client is not a finished transaction.** Its server session, waiting for a lock, goes on and may commit later. End it and wait until it is gone before judging the data.
- **Test the exits on the real thing.** The unit tests passed at once; the cut-rows case on 18, the hang and the kill each found a bug.

## Keep doing
- For every destructive step: what does the record say if the process dies right before, during and right after it?

## Open
- `--abandon` needs the running server.
- A restore on an external PostgreSQL 18 with pg_textsearch always fails (#1671).
