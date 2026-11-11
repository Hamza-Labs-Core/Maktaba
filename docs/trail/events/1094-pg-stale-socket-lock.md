---
schema: 1
kind: incident
pr: 1094
title: "Bundled Postgres crash-looped after a kill: the socket's stale lock with a reused PID"
date: 2026-11-10
session: 1ae75261-b8cb-4a4e-bb1d-0ed6d6aad03f
agent: claude-code
---

## What happened
- **Found by** the NAS CI (story 59.15, PR 1091): after `docker kill` and a start on the same
  volume, the bundled Postgres never came up again. It crash-looped on `FATAL: lock file
  ".s.PGSQL.54329.lock" already exists`.
- **Cause:**
  - A container started again gets a new PID namespace, so the old postmaster's PID soon
    belongs to another process.
  - Postgres takes over a lock only when its PID is gone, its own or its parent's. For
    `postmaster.pid` it also checks the shared memory. For the socket lock in the data
    directory it checks nothing else.
  - `pgbundle.ClearStaleLock`, run before each start, handled only `postmaster.pid`.
- **Impact:** a power cut or a forced stop could leave a NAS (or any Docker home) without its
  database until someone deleted the lock by hand. main had this for every Linux container
  install.
- **Fix** (merge 15b032d88):
  - `ClearStaleLock` also removes the socket lock when its PID is a live process and
    `postmaster.pid` names no live postgres. A postmaster takes the data directory's lock before
    its socket's, so in that state no postmaster owns the socket.
  - It keeps a `postmaster.pid` whose shared memory segment (line 7) still has a process
    attached. Postgres makes that check itself, and removing the file would skip it.
- **Proof:**
  - `TestRealRestartAfterAKillWithItsPIDReused` ran in the replica tier's `TestReal` step on
    real Postgres. It SIGKILLs a postmaster, hands both locks' PID to a live process,
    reproduces the FATAL, then starts the cluster on the same data after `ClearStaleLock`.
  - First run locally in a Linux container (golang:1.25-bookworm, Postgres 15).

## What we learned
- Postgres's two lock files are not equal: only the data directory's lock has the shared
  memory fallback. A PID-reuse fix that clears one lock leaves the other crash-looping.
- The order in which Postgres takes its locks (data directory first, then the socket) is
  what makes the socket lock safe to judge from `postmaster.pid`.
- A real-Postgres test can reproduce a container's PID reuse without Docker: kill the
  postmaster, then write a live helper process's PID into the lock files.

## Keep doing
- Prove crash and restart paths with a real-Postgres Go test in CI (the replica tier runs
  `TestReal*` with `-v`, so a skip shows), never on the owner's live home.

## Open
- The NAS CI's `restart-after-kill` cell (`needs: ["pg-stale-lock"]` on PR 1091) can now be
  switched on.
