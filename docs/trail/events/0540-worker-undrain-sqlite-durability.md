---
schema: 1
kind: pr
pr: 540
title: Worker agent undrain timeout on Windows: SQLite durability cost in test DBs
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- `unit / pipeline-cluster-windows` failed on #526: `test_a_home_worker_runs_its_agent[sqlite]` timed out (3 s) waiting for the `undrain` `cmd.result`. The handler was still running, not losing the reply.
- Cause: on windows-latest each SQLite write cost 0.3-1.8 s (a sync per commit plus a journal file per write). The engine's background loops share one connection behind an asyncio lock, so undrain's `UPDATE workers` waited behind them. Product checked: `_command` always replies.
- Fix (#540, merge 90faa54d): `tests/engine/fixtures_db.py` sets `synchronous=OFF` and `journal_mode=MEMORY` for test DBs, guarded by `tests/engine/test_fixtures_db.py`. Cluster suite 3-5 min to 44 s; the test 10 s to 0.67 s.

## What we learned
- On SQLite every engine loop shares one connection behind an asyncio lock, so one slow writer delays all the others.

## Open
- The reconciler held the shared SQLite connection for its whole pass in one transaction (about 10 s at 100 ms per statement), enough to time out the 8 s heartbeat (fixed in #561).
