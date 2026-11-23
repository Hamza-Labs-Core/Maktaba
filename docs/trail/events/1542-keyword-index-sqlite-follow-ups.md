---
schema: 1
kind: pr
pr: 1542
title: "49.6 follow-ups: SQLite's FTS5 rows by key, no search_index loop on SQLite, a portable speakers hook"
date: 2026-11-23
session: 7998f2a8-8a3e-41cf-99b3-4cf9a473ddc3
agent: claude-code
prs: [1542]
---

## What happened
- #1542 takes up issue #1483 (49.6's review minors) and #1477 item 5, while 49.13 waits for
  the home's update.
- Keying the FTS5 row by its document's rowid needed no migration, since an upsert keeps the
  rowid. Rows written earlier, though, carry FTS5's own rowids (1, 2, …), and one of them
  could equal another document's rowid. A point delete would then remove the wrong video's
  row, and an insert would collide. The new keys therefore sit above 2^40, and an earlier
  row goes by its `video_id`, once.
- The SQLite pipeline's `execute` returns no row count. The rewrite asks by key whether a
  row exists, a point lookup, before it chooses how to delete.
- #1477 item 4 (a UTF-8 guard in 0231) is left. Without `maktaba_stem`, the later migrations
  that build indexes on it would fail instead.

## What we learned
- To re-key existing rows without a migration, put the new keys in a range the old ones
  cannot reach.

## Open
- #1477 items 1 to 3 (stemming rules) change the index, so they need a migration and a
  reindex. They wait for the home's update.
- #1492 (the phrase boost and the page's lines measured on the home's own lines) waits for
  49.9's home run.
