---
schema: 1
kind: pr
pr: 1424
title: "Plugins can no longer starve the API's database pool: a pool of their own, a timer reaper, a Postgres backstop"
date: 2026-11-15
session: aa4f6da6-177c-478d-81fa-7ab103e7cac3
agent: claude-code
---

## What happened
- On the live home (0.3.135), sign-in hung: `POST /api/auth/login` waited for a connection
  from the API's pool of 50, and none came free. The first-party plugins' statements
  executor took its connections from that same pool. It kept up to 4 role connections per
  plugin between calls, and 10 plugins use it (about 40). Six transcription transactions
  from the worker on the other machine had been idle in transaction for 19 hours. `reap()`
  ran only when a new `begin` arrived, and skipped any transaction a call held.
- The same starvation showed as `/internal/plugins/invocations` 500s after 15 s,
  `queues_snapshot_build_failed` and `cluster_cache_failed` in the server log.
- Recovery on the live home: restarting the API released every connection. Login answered
  in 0.5 s afterwards.
- #1424 merged with 160 checks green:
  - the home's executor has a pool of its own (`statements.Open`, 12 role connections plus
    4 for lookups); without a DSN, it holds at most `MaxConns` of the shared pool
  - every held connection takes a slot; a call with no slot gives back the oldest kept
    connection, else waits at most 10 s and fails as retryable (503 `plugin_database`)
  - a timer reaper rolls back transactions idle past 2 min and returns kept connections
    unused for 1 min, with no new `begin` needed
  - ending a transaction never waits for a call that holds it; a call inside a transaction
    runs at most 5 min
  - each role session sets `idle_in_transaction_session_timeout` (195 s) as a backstop
  - `Executor.Stats()` goes to `/metrics` and to the admin-only `plugin_relay` in health
- In review, a double-commit race turned up and was fixed in the same PR. Two calls that
  both looked up one transaction: the first committed and gave the connection back, then
  the second committed again (ErrTxDone) and discarded it a second time, freeing a slot it
  never held and resetting a connection another call could be using. `lockTx` now refuses
  a transaction already done. `TestRelayACallAfterTheCommitIsRefused` fails without the
  fix and passes with it.

## What we learned
- A component that borrows from a shared pool needs its own cap or its own pool. Kept
  connections are invisible in the pool's own stats until it is empty.
- Cleanup that runs only on the next request never runs when requests stop. Idle state
  needs a timer, plus a database-side timeout as a backstop.
- With `done` and `doomed` flags under a lock, every path that takes the lock has to check
  both: a caller can look the object up before another caller finishes it.

## Keep doing
- Write concurrency tests that recreate the interleaving directly (look up, then act)
  instead of relying on sleeps.
- Report the starved resource's holders (held, kept, waiting, oldest transaction) to
  admins, so the next starvation is visible before login hangs.

## Open
- The live home stays on 0.3.135 until it updates to a release with #1424.
- A web panel for `plugin_relay` (needs English and Arabic strings); the data is in the
  health JSON and on `/metrics` today.
