---
schema: 1
kind: pr
pr: 1727
title: "An own PostgreSQL 18 with pg_textsearch can put a backup back, and the restore's smaller follow-ups"
date: 2026-11-25
session: af4db1a0-1123-496c-869f-9a21bd3c8aad
agent: claude-code
prs: [1670, 1698]
---

## What happened
- **#1671 does not touch a bundled home.** Checked with the real migrations, both BM25 indexes and rows on 18.7: dump, restore, rehearsal. The bundled restore has left the `textsearch` schema out since pg_textsearch came.
- **It touches an operator's own 18:** the restore makes only `public` again, and the dump's `CREATE SCHEMA` for another schema failed. What makes the schemas and extensions that stay is left out now (`pg_restore --use-list`), and CI runs `dbsafe` on 18 too.
- The minors of #1681 and #1688 a person can hit: the order of the refusals without psql, the use of `public`, moved backups, the first backup's links, what a confirmation covers, a warning on the Updates page.

## What we learned
- **A test rig can hide the bug it should find.** The rig of every way out dropped pg_textsearch from its "external" Postgres, as "a person's own has none", and so passed on 18. The rig now keeps it.
- **CI ran the package on 16 only,** so the four tests that fail on 18 were seen only by hand.
- **A fake program that ignores its arguments breaks when they matter:** the killed restore's `pg_restore` had its own argument list and never got `--use-list`, so on 18 it failed as if the fix were missing. A fake passes on what it was given.
- **pgvector is not a trusted extension.** A home whose database user is no superuser cannot make `public` again with it: no backup can be put back there (#1720). Found by asking what else `DROP SCHEMA public CASCADE` takes along.
- **`pg_available_extension_versions.superuser` is not what a hosted Postgres allows,** so it decides only where a wrong try cannot be undone (no psql); elsewhere the server says it and the transaction rolls back.
- **A schema made again has no grant for PUBLIC.** A dump holds a grant on `public` only where it differs from a new database's, so the restore has to give that one itself.

## Keep doing
- Answer "can a real home hit it?" with a probe of the real thing (real migrations, real indexes, real programs) before fixing.

## Open
- #1720; the rest of #1681 and #1688.
