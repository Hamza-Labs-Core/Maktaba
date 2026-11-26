---
schema: 1
kind: pr
pr: 1775
title: External restore preflight and PostgreSQL recovery
date: 2026-11-26
session: 01a218c5-8e0d-71e0-bebb-8bcbb2172778
agent: codex
---

## What happened
- [PR #1775](https://github.com/Hamza-Labs-Core/Maktaba/pull/1775) merged to main as `800f805fc01f1a711830d849e4b27723b97ce5fd`, closing [#1736](https://github.com/Hamza-Labs-Core/Maktaba/issues/1736), [#1688](https://github.com/Hamza-Labs-Core/Maktaba/issues/1688), [#1681](https://github.com/Hamza-Labs-Core/Maktaba/issues/1681), and [#1689](https://github.com/Hamza-Labs-Core/Maktaba/issues/1689).
- External restores now reject archives that cannot be restored in one transaction before dropping objects or ending replication slots. Recovery can discard a restore deferred to another release. PostgreSQL 16-to-18 recovery bounds grant replay and handles stale shared memory and Windows lock paths outside the ANSI code page.

## What we learned
- Record the safety dump path before creating the dump so a retry after interruption can reuse it. Startup distinguishes shared memory held by another live server from memory left by a killed postmaster. The former stops the wait, while the latter can trigger an immediate supervisor retry.
