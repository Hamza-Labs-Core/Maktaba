---
schema: 1
kind: pr
pr: 1691
title: "Every private Postgres start waits out a killed postmaster's shared memory, on Windows too"
date: 2026-11-24
session: af4db1a0-1123-496c-869f-9a21bd3c8aad
agent: claude-code
prs: [1607]
---

## What happened
- `TestRealMajorUpgrade` failed on Windows in #1607's CI (#1676): the 16 to 18 upgrade's recovery of a cluster stopped hard started once and met "pre-existing shared memory block is still in use". A real home in that state stays on 16 until its next start.
- `withServer`, the one place every private start goes through, now clears a stale lock, waits (`AwaitShmFree`), and starts again when Postgres says the memory is in use, up to `shmWait`.
- On Windows `shmInUse` always answered no. It now opens the named mapping Postgres makes for the data directory.

## What we learned
- **The earlier wait covered one caller.** The supervisor's start waited; the upgrade, `Prepare`, an offline restore and a moved library's check did not. The shared start was the place for it.
- **A look before the start is not enough on Windows.** A refused start has already replaced or removed `postmaster.pid`, so the next look has nothing to read. Postgres's own words are the only sign left, so a private start is also retried on them.
- **The mapping's name** is `Global/PostgreSQL:` and the data directory with every backslash a slash, the one after `Global` too. A test asks for it while a real server runs, since a wrong name looks the same as free memory.
- **A test that holds the memory** needs the postmaster killed while its children are stopped (SIGSTOP), and a log file, not a pipe: the stopped children keep a pipe open and `Wait` never returns.

## Keep doing
- Write the integration test so it first shows the old behaviour (one start, refused) and then the new one, on the same held memory.

## Open
- The review's minors: #1689 (a false wait when a stale lock's segment id is another live Postgres's; non-ASCII data directories on Windows; the supervisor's start on Windows after a refused start; a wait a stop cannot end).
