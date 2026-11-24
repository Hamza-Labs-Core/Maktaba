---
schema: 1
kind: pr
pr: 1587
title: "The cloud's shared rate limit decides from the token it took, not the time it wrote"
date: 2026-11-24
session: e17bc07f-84d4-4215-9722-3af0a39bc002
agent: claude-code
---

## What happened
`TestPGConcurrentTakesExactlyTheBurst` allowed 31 of 90 concurrent requests with a burst of 30 in CI run 45327359342, on #1568, which touches no cloud code. #1583's trail event (#1584) left it open.
- **Root cause:** `takeSQL` in `cloud/internal/ratelimit/pg.go` answered `RETURNING updated_at = now()`. A refused take leaves `updated_at` unchanged, so it read as allowed whenever its transaction's `now()` equalled the last allowed take's. Ninety requests starting at once over three pools can share a microsecond.
- **Second fault:** a take whose transaction started before the last take (it waited on the row's lock) moved `updated_at` back, so the next take refilled that gap again.
- **Fix:** `ON CONFLICT … DO UPDATE … WHERE <a token is there> RETURNING true`; no row is a refusal. `updated_at = GREATEST(b.updated_at, now())`.
- **Tests:** `pg_take_integration_test.go` reproduces both inside one transaction, where every statement shares one `now()`. Both failed on main's statement. The concurrent test then passed 30 runs in a row on PostgreSQL 18.7.
- An independent review confirmed it with two live sessions (a refusal after a wait; a new-key insert race at capacity 1). It found nothing to change.

## What we learned
- **`now()` is the transaction's start time, not a unique stamp**: never read "did this statement write" from a timestamp it may or may not have written. Return the decision itself; `ON CONFLICT … DO UPDATE … WHERE` returns no row when the WHERE is false.
- **A flaky concurrency test can be made deterministic in one transaction**: every statement there shares one `now()`, which is exactly the collision a burst only sometimes hits.
- **A scratch PostgreSQL needs no Docker on this Mac**: the Maktaba app bundles PostgreSQL 18 (`/Applications/Maktaba.app/Contents/Resources/server/postgres-18/bin`). Use `initdb` into the scratchpad, TCP only (`unix_socket_directories=''`, since the scratchpad path is over the 103-byte socket limit), then stop it and remove the data directory.

## Keep doing
- Chasing a CI flake in code the pull request does not touch to its root, in its own pull request, rather than only rerunning it.

## Stop doing
- Deciding a statement's outcome from timestamp equality.

## Open
- Two older behaviours of `PG.Allow`, both kept, are noted here only: a client that cancels its request still spends one of the node's own fallback tokens, and a new key is always allowed (only matters below a capacity of 1, which no caller uses).
