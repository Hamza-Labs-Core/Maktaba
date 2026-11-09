---
schema: 1
kind: pr
pr: 1077
title: The customer sign-in timing hash is made at the first sign-in
date: 2026-11-09
session: 94fe1832-65cc-4ea1-93a8-24d8cc8c5ec1
---

## What happened
- This closes the open item from #1027. The customer auth handler (`cloud/internal/handlers/auth`) made its argon2id timing hash (64 MiB) at package init, so every role of the cloud binary held it, the relay too. It is now a `sync.OnceValue`, as in staff sign-in.
- Timing equalisation is unchanged: an unknown email, or an account with no password, still verifies against the hash.
- Tests: a counter inside the once is read in `TestMain` after init and must be 0. Putting the eager call back makes that test fail. `Login` runs for an unknown email through a no-rows `database/sql` driver: it must answer 401, make the hash, and take at least half a real verification's time.
- load-small's memory per tunnel for this change is in #1077's cloud-rehearsal run, which compares the PR against its base.

## What we learned
- A counter in the once, read in `TestMain` before `m.Run`, is enough to prove that a package's init did no expensive work. It needs no heap measurement, so it can't be flaky.
- A tiny `database/sql` driver that returns no rows lets a handler that takes a concrete `*stores.Users` run its not-found path in a unit test, with no Postgres.
- The cloud unit tier runs `go test -short` without `-race` or the budget tool. A real 64 MiB argon2id in a cloud unit test costs about 0.3 s there; under `-race` it would take several seconds.

## Keep doing
- When a fix lands in one handler, look for the same pattern in its siblings. Both sign-in handlers had the eager hash.

## Open
- None.
