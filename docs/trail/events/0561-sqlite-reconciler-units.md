---
schema: 1
kind: pr
pr: 561
title: The SQLite reconciler frees the shared connection between units
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge 62213ab6. Found by #540: on SQLite every engine loop (reconciler, pruner, channel top-up, queue health, heartbeat) shares one connection behind an asyncio lock, and `reconcile_once` ran its pass in one transaction. A 1000-row sweep held it about 10 s, past the 8 s heartbeat, and stalled undrain.
- Fix: on SQLite no transaction around the pass; each unit commits on its own (one enqueue via `_enqueue_sqlite`, one legacy repair via `finalize_success` guarded by `propagated_at IS NULL`, one `propagated_at` mark). Steps select only what is left, so the next pass finishes a stopped one. Postgres unchanged.
- Also fixed in `Database.transaction` (SQLite): a cancel during `BEGIN IMMEDIATE` left the connection inside a transaction forever, and a cancel while COMMIT/ROLLBACK was queued released the lock early. Both now run to their end under the lock, and a closed connection never turns the cancel into an error (a first cut hung the pruner loop).
- Tests: a statement hook per task (heartbeat waits for at most 12 reconciler statements, was 999; a pass cancelled after each statement leaves only whole units) and cancelled BEGIN/COMMIT tests.

## What we learned
- A task created inside a transaction inherits `_TX_CONN` and bypasses the lock.
- Cancellation during BEGIN or COMMIT must be shielded so the transaction state and the lock stay consistent.

## Open
- Postgres pass in short transactions (done in #570); numpy missing from dev extras and `test_paths_per_os` failing once (both in #572).
