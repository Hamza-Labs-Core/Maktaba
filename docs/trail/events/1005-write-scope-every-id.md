---
schema: 1
kind: pr
pr: 1005
title: Every first-party write checks each id that binds its rows (README §15.12)
date: 2026-11-08
session: 4b7e8b40-71f5-4d69-a83b-8b9db3ccf5a1
agent: claude-code
---

## What happened
A first-party plugin's write statement named one scope argument, or none, while it reached
rows by other ids too. 91 of 187 write statements in the bundle (78 distinct texts) needed
more checks than their header listed; about half had been `catalog`, meaning no check at all.
Examples: `UPDATE series_seasons … WHERE id = $1`, link's YouTube upserts,
`detect.apply_changes` (`ON CONFLICT (id) DO UPDATE SET library_id = EXCLUDED.library_id`
could take another library's series), and the file-action ledger's `video_id = CASE WHEN $27
THEN $28 …`. The build now works the checks out from the parse tree and
`tools/plugins/scope_tables.toml`, writes them all in the header
(`scope: subject $1, library $2, season $4`), and fails any write it cannot bind. The API runs
each check, looking up the new row kinds in `statements/resolve.go`. The worker's relayed
`log_event` and `dedup_commit` (`keeper_library` was unchecked), and the API's `writeScope`
and `suggest/add`, were tightened the same way.

## What we learned
- Only two writes needed checks set by hand: `pgstore.write_vectors`, whose keys come from
  `unnest($5)`, and `transcribe.forget_gone`, which takes no argument. The parse-tree rules
  (a top-level `AND` term on an owner column, every owner column of an `INSERT`, the conflict
  target of `DO UPDATE`) cover the rest.
- A `CASE WHEN $flag THEN $value` sets an owner column from `$value` only. Checking the flag
  would refuse every call (a boolean is not an id).
- A first-party token has `AllLibraries = true`, so `writeScope`'s granted-library test let a
  library job write any library's video. A first-party job's own library is `inv.LibraryID`.
- YouTube rows are shared: a channel belongs to the first library that claims it, while
  others follow it through `youtube_sources`. A strict owner check would have broken a second
  library's sync, so a YouTube row counts as in scope for every library that follows or
  downloads it.
- Bigint ids (segments, audio tracks) follow a predictable sequence. A cached "no such row,
  allowed" could be replayed once another library creates that id, so only found rows are
  cached.

## Keep doing
- Run a new guard against open wave branches before they merge. On #998's branch it found
  6 unlisted tables and 8 bare-string `[scope]` overrides that the analysis now derives.
- Walk the embedded bundle in a Go test, with one seeded row per kind in two libraries, so
  every declared check is proven to refuse and to allow.

## Open
- #998 (wave 9f, core-media) needs `statements_build.py sync core-media` (7 headers) and its
  `[scope]` table removed once this merges. Its tables are already in `scope_tables.toml`.
- Reads by id across libraries (first-party tokens read every library) are not scoped. Writes
  only for now.
- The worker's `publish` relay op still takes any payload.
