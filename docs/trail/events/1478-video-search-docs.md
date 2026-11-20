---
schema: 1
kind: pr
pr: 1478
title: "video_search_docs and its BM25 indexes, with pg_textsearch in its own schema"
date: 2026-11-20
session: 753035df-3990-44b1-9dff-f305bbe9c3cd
agent: claude-code
prs: [1476]
---

## What happened
- #1478 merged as 9f2fbf071. Epic 49 story 49.5.
  - Migration 0232 adds `video_search_docs`: one keyword search document per video, with a
    btree on `library_id`. The table is left out of a machine's copy.
  - It adds `maktaba_search_index_statements()`, which returns the `CREATE INDEX
    CONCURRENTLY IF NOT EXISTS` for each missing BM25 index: an exact one on
    `maktaba_normalize`/`maktaba_search`, and a stemmed one on
    `maktaba_stem_prepare`/`maktaba_stem`.
  - SQLite gets the table and an FTS5 table keyed by `video_id`.
  - The bundled server creates pg_textsearch, as the superuser, in the schema `textsearch`.
  - A dedup merge drops the losing copy's document.
- GLM-5.3's first review: one blocker, that the replica step ran the wrong test package. It
  was fixed, with two minors; the rest are issues #1479 and #1483.
- CI found two bugs that the reviews and local tests missed:
  - **A rule for the new foreign key.** `test_every_foreign_key_to_videos_has_a_rule` fails
    on any table pointing at `videos` that has no dedup merge rule.
  - **Fresh homes could not migrate.** Nearly every NAS container cell failed setup at slot
    0103. The bootstrap made pg_textsearch in `public` before the migrations. Slot 0103, as
    the app role, grants EXECUTE on every function of `public`, and a few of pg_textsearch's
    functions are the superuser's alone. Granting on a function the granting role cannot run
    is an error, not a warning. Fixed with a schema of its own; a restore leaves that
    schema's entry and its grant out.

## What we learned
- A migration test that migrates as a superuser cannot see a privilege bug. `pg18-check.sh`
  now migrates as an app role that is no superuser, as a home does, and with the extension
  back in `public` it fails exactly as the cells did.
- An extension whose functions revoke PUBLIC's EXECUTE must not live in `public` here.
  `relocatable = false` only stops a later move; `CREATE EXTENSION ... SCHEMA s` still
  works. Its operator then needs `OPERATOR(textsearch.<@>)`.
- `CREATE DATABASE` cannot share one `psql -c` with another statement (an implicit
  transaction).
- The NAS cells keep no container logs once the container is gone. The fastest diagnosis
  was reproducing the setup locally: a fresh 18 cluster, the bootstrap's extensions, and
  `migrate up` as a non-superuser.

## Open
- Issue #1479: the BM25 build in `migrate up` is solved by #1482 (story 49.6).
- Issue #1483: review minors.
