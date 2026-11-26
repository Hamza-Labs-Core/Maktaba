---
schema: 1
kind: pr
pr: 1776
title: Cloud security retention and additive backfill rollback
date: 2026-11-26
session: 01a218c5-8e0d-71e0-bebb-8bcbb2172778
agent: codex
---

## What happened
- [PR #1776](https://github.com/Hamza-Labs-Core/Maktaba/pull/1776) merged to main as `574eb92fb07e07c02f0bd63077142c28ea72e408`, closing [#1693](https://github.com/Hamza-Labs-Core/Maktaba/issues/1693). The work was part of [#1731](https://github.com/Hamza-Labs-Core/Maktaba/issues/1731).
- The Cloud worker sweeps audit records after 90 days and abuse signals after 30 days. Account erasure removes security records directly linked to the account or its homes.

## What we learned
- A migration backfill can roll back when its writes are confined to columns added by that migration and its Down script drops those columns. Dropping the added columns reverses the backfill along with the schema change.
