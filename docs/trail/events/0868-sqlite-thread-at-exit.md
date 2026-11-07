---
schema: 1
kind: pr
pr: 868
title: A closed SQLite database waits for aiosqlite's thread
date: 2026-11-07
session: 03d72f4f-f05c-4d72-897b-0e3d0c10f530
---

## What happened
- On #781, `test_run_hands_its_mode_to_the_stage_handlers` reached `os._exit(0)`, which is stubbed in tests. `_serve`'s exit counted `_connection_worker_thread`, the worker's own aiosqlite thread, as still running.
- `Database.close()` now joins aiosqlite's thread. The exit gives threads that are already ending a second in all before it counts them.

## What we learned
- aiosqlite's `close()` returns when its thread has said it stops, a moment before the thread has ended. A count of live threads right after it races with the thread's own end.
- Since 2026-11-02 the conftest's teardown check fails any test that leaves a non-daemon thread running. A thread a later test sees therefore started inside that test: no earlier test leaked it.

## Keep doing
- Join a library's worker thread when closing a resource whose end the process counts.

## Open
- Nothing.
