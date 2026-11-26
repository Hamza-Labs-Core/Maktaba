---
schema: 1
kind: pr
pr: 1770
title: Updates reliability follow-ups — feed freshness, schema_release, copies_paused keepalives, and copy-wait fixes
date: 2026-11-26
session: 4419ee9d-62d2-421a-9d13-992d2c6e9711
agent: antigravity
---

## What happened
- Closed #1668 (release feed freshness): kept `releaseverify.FeedMaxAge` at 21 days as the hard ceiling and added `releaseverify.FeedFreshAge` (8 days) so feeds aged 8–21 days still apply withdrawals and cross-check GitHub, capping `LastCheckOK` at the feed signing time and recording `feed_old` when GitHub is unreachable; added English and Arabic `feed_old` wording across Web, Apple, and Android.
- Closed #1666 (`schema_release` in guard record): added `SchemaRelease`, `DumpSchemaRelease`, and `SchemaOwner()` to `apppayload.Database` and updated `keptFor`, `migrationGate.run`, `backWithoutDump`, `restoreAsked`, `serve_restore.go`, and `layout.MarkRecordDown` / `UnmarkRecordKept` so intermediate releases remain allowed after Down scripts or restores while preserving backwards compatibility with legacy records.
- Closed #1667 (replication `copies_paused` and TCP keepalives): configured Postgres TCP keepalives (`30s / 10s / 3`) for bundled and operator Postgres, bumped `clusterRev` to 7 and `SchemaRev` to 239 with migration `0239_copies_paused.sql` (`cluster_copies_paused()` and `copies_paused` in `cluster_node_replica_grant()`), and updated `nodecopy` so paused copies poll every 5 s (`CopyPausedEvery`) without accumulating auth-refusal backoff.
- Addressed #1753 items 1, 2, 4, and 5: clarified `machine_copying` EN/AR wording on Web and Android, cleared `Service.copySince` outside the quiet window via `machineCopying(ctx)` without polling unrelated blockers or `HomeHealth`, localized missing refusal codes (`tv_not_held`, `no_such_run`, Check now 502/503, codeless 500s) in a shared `updateRefusal()` helper across Admin → Updates and Live TV's `HeldReleaseCard.tsx`, and audited `copy_wait_over` only after `apply` succeeds using actual copy-wait duration.
- Merged as `f2b8cd434` with all 204 checks green.

## What we learned
- `TestMigrationsDownAfterPartialUp` compares `pg_get_functiondef` bodies byte-for-byte after running a migration's Down script; replacing an existing SQL function in a new migration requires the Down script's `CREATE OR REPLACE FUNCTION` body (including internal SQL comments) to match the previous migration verbatim.
- Clearing a copy-hold outside the quiet window must check only `CopyingBlocker` (`machineCopying`), not the full `blockers()` helper, so an unrelated blocker (`tv_busy`, `switchover_open`) or unreachable `HomeHealth` never prevents `copySince` from resetting between days.
- In `nodecopy.Manager.watch()`, latching `everPaused` across `report()` calls ensures unpausing during a `CopyPausedEvery` sleep returns promptly instead of sleeping through the full backoff interval.
- Scoping codeless 502/503 fallback wording strictly to `Check now` (`check: true`) avoids mislabeling unrelated update/apply 502/503 errors as GitHub release-check failures.

## Keep doing
- Diff any Down migration that restores an older SQL function byte-for-byte against the prior migration's function body before pushing.
- Run `go test -race` locally on pure-Go concurrency tests (`nodecopy`) before pushing to catch data races early.

## Open
- #1753 remains open for item 3 (evaluating whether to add a second Rehearsal 17 run from `v0.3.162` at ~6 minutes of CI time).
