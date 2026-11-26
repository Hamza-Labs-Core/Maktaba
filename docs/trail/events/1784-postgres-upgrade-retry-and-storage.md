---
schema: 1
kind: pr
pr: 1784
title: PostgreSQL upgrade retries and set-aside storage
date: 2026-11-26
session: 01a21956-bf2a-7aa2-b815-9dc4f89b27ee
agent: codex
---

## What happened
- [PR #1784](https://github.com/Hamza-Labs-Core/Maktaba/pull/1784) merged to main as `dd7954d2061eb6eb41e29ec1092df2b14102be14`, closing [#1457](https://github.com/Hamza-Labs-Core/Maktaba/issues/1457) and [#1473](https://github.com/Hamza-Labs-Core/Maktaba/issues/1473).
- PostgreSQL 16-to-18 retries take a fresh safety copy and preserve only strictly newer, untried PostgreSQL 18 restore requests. The upgrade writes `UpgradeMarker` atomically, warns when the source was replaced, and sweeps abandoned leftovers.
- `storage.Report`, `storage move`, and `storage show` account for set-aside clusters, including the exclusion when moving custom storage back to the default. `serve_copy.go` distinguishes the pre-join database from an old replica.

## What we learned
- A live PID can belong to a recycled non-Postgres process. Identify the process before sweeping and protect live staging directories.
- `backupsFolderProblem` resolves existing symlinked ancestors even when `StateDir` has yet to be created. Reject bundled and set-aside cluster paths while allowing `postgres-backups`.
