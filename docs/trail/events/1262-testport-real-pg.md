---
schema: 1
kind: pr
pr: 1262
title: "Real-Postgres tests take their ports from testport"
date: 2026-11-14
session: e509bd7a-d0fc-4d82-b063-635c25c29735
agent: claude-code
---

## What happened
- #1262 merged as 5b18b26f1. devmigrate's `TestRealMove` failed on #1242, which doesn't touch it,
  with "could not bind IPv4 address: Address already in use" on port 39217.
- devmigrate, nodecopy, pgbundle's Windows token test and supervisor's Windows stages e2e now
  take `testport.Free` (ports below every system's ephemeral range, each held by a lock file).
- A new guard (`cmd/maktaba-server/internal/testport/guard_test.go`) fails when a test starts a
  bundled Postgres on a port taken with `net.Listen("tcp", "127.0.0.1:0")`.

## What we learned
- Listen on :0, close, hand the number to a child process is a race: the port is ephemeral, and
  any outgoing connection from a parallel test package can take it first. testport (96740f8af)
  fixed pgbundle and pgtest, but five files kept their own helpers; only a guard keeps it fixed.
- A unit job can also fail on test-budget's hard soft-cap (`TestMachineRoutesAdminOnly` took
  2.12 s alone on a loaded runner, 0.07 s locally); re-running the failed job cleared it.

## Keep doing
- When a fix replaces a pattern, add a guard test that fails on the old pattern anywhere.

## Open
- Nothing.
