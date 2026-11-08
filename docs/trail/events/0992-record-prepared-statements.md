---
schema: 1
kind: pr
pr: 992
title: "53.12: Record prepares its upserts once per scrape; TestOverviewManyMachines under its soft cap"
date: 2026-11-08
session: 0e9a3649-4e54-455d-9465-045c6636722d
prs: [983]
---

## What happened
- `Record` (`shared/logkeeper/go/logindex/metrics.go`) prepares its two upserts once per scrape and runs them per row, in the one transaction it already used. Rows, their order and the rollback on a failed row are unchanged; every home's scrape is cheaper.
- `TestOverviewManyMachines` under `-race` went from 0.48 s to 0.31 s on its first run (the shared fixture's recording from about 300 ms to about 140 ms); no assertion and no cap changed, and no TestMain was added.

## What we learned
- `database/sql` over modernc SQLite keeps no statement cache: each `Exec` with arguments parses its SQL again. A loop that runs one statement per row prepares it once.
- A CPU profile put about half of `Record`'s time under `-race` in SQL parsing. The 0983 event's reading, that it was mostly WAL locking, was not what the profile showed.

## Open
- The first run keeps about 90 ms under the 400 ms soft cap; what is left is the board read itself (about 0.16 s).
