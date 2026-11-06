---
schema: 1
kind: pr
pr: 669
title: "Epic 56: the TV library view (Play all, Shuffle, series, collections, playlists)"
date: 2026-11-06
session: local_74545651-2fc4-4693-ac62-08aec457c516
---

## What happened
One server play queue (`/api/me/play-queues`, slot 0191) behind every Play and Shuffle on
the web, both TV apps and both phone Players; smart playlists (0192); collections kept in
step from YouTube playlists and channels and from Plex (0193, two pipeline stages); a
library's collection mode and promoted rows; `group=series`, categories; OpenAPI for every
library route with a coverage test. Closes #654.

## What we learned
- The queue reads each source through its own list route, called in process as the caller
  (`playqueue/sources.go`): the order and the access rules can never drift from the grid's.
  Shuffle over a whole library is `GET /api/videos?sort=shuffle&seed=N` (md5 of id and seed,
  paged with the list's cursor); SQLite gets `md5()` registered with the modernc driver.
- Main took slots 0184-0190 while this was in flight (the queue engine's lock order, the
  component restarts, Epic 52's word review in #672, the import dry run in #678 and the
  restart table's fix in #692): the three migrations moved three times, to 0191-0193 at the
  end, with SCHEMA_REV / SchemaRev at 193
  in the pipeline, the API and the streaming node, and the epic's spec table updated. A
  branch that adds migrations waits for the PRs holding the slots below its own: the gaps
  lint fails on an empty slot under the highest.
- The migration lint wants `CREATE INDEX CONCURRENTLY` even on a table the same migration
  makes; a migration with one runs as `-- +goose NO TRANSACTION`, each statement idempotent.
- The integration job runs goose up on an already migrated database: an `ADD CONSTRAINT`
  needs a `DROP CONSTRAINT IF EXISTS` before it.
- An extension's stages must sit in its own `ext.*` component (a catalog test), so the Plex
  import is `ext.plex`'s, not a `collections.plex` of its own.
- A vitest `beforeEach(() => mock.mockReset())` returns the mock, which vitest then calls as
  a teardown: write the block with braces.
- On Android TV a `TabRow`'s FocusRequester focuses its first child, which in Arabic is the
  last tab: the requester goes on the selected Tab.
- A Compose `LazyColumn` item that draws nothing still takes its spacing: add the item only
  when it has something to show (the Player's Continue your queue).

## Open
- The letter rail jumps by filtering (`letter=`) rather than as a cursor seed, and the Android
  TV keeps a library's last tab in SharedPreferences, not DataStore.
- The 150,000-video queue benchmark (56.01 T11) is not written; the 1,300-move walk on CI's
  Postgres runs at about 11 ms a move.
