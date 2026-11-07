---
schema: 1
kind: decision
title: A rollback runs the release's Down scripts, the dump only as a last resort
date: 2026-11-07
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
---

## What happened
- 0.3.90 rolled back with watch_timeout. Its only migration, 0192, added two columns and two CHECK constraints to user_playlists.
- The schema-keep rule counts any ADD CONSTRAINT as destructive, so the rollback put the whole 73 GB database back from the pre-update dump. Every table was reloaded and every index rebuilt; the HNSW index over 28 M vectors alone needed about a day. Everything written since the dump was lost, and the home was down the whole time.

## What we learned
- Every migration has an Up script and a Down script (goose), and `maktaba-api migrate down-to` already existed. The rollback never used it.
- A rollback should be targeted. When the schema cannot be kept but each migration since the release before has a Down script and loses no data on the way up (no DROP of a table, column or sequence; no DELETE, UPDATE, TRUNCATE or type change), the new release runs `migrate down-to <previous schema>` with its own migrations. Only the tables those migrations touched change. The record becomes the previous release's own (layout.MarkRecordDown).
- The dump goes back only when an Up script lost data or a Down script fails.
- With the owner's OK, the home was put back that way by hand at 15:43Z. 0.3.90 had 14 migrations (0184–0197), not one. 0.3.90's own `maktaba-api migrate down-to 183` ran all 14 Down scripts in about 5 s. The record was set to v0.3.82, migrated, and the restore (12% into the HNSW build, about 20 h left) was stopped along with its one-worker retry. 0.3.82 started on the 73 GB database with every index and the day's writes kept.
- 0184 and 0189 DELETE inside plpgsql function bodies. The data-loss check skips dollar-quoted bodies: a function a migration defines does not run then.

## Keep doing
- Write a Down script for every migration that undoes exactly its Up.
