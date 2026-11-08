---
schema: 1
kind: pr
pr: 1025
title: Every plugin-API read by id is checked against the job (README §15.12)
date: 2026-11-08
session: 5cad87d9-5ef8-4d36-b6d8-fa75828cf71a
agent: claude-code
---

## What happened
#1005 checked every id that binds a first-party write; reads stayed unscoped. Most read
statements were `catalog` or carried one `subject`/`library` check, picked by a heuristic that
took any column named `video_id` or `library_id`. A plugin could read another library's season,
series, transcript, collection, job or duplicate group by passing its id, and a first-party
token (`AllLibraries`) reached every library through `library/*`, `transcript/*` and
`media/info`. `write_scope.read_checks` now types every comparison a statement makes between a
column and a `$N`, anywhere in it (WHERE, a join's ON, a subquery, an OR, a write's source
rows), against `scope_tables.toml`. The heuristic is gone. 123 statement files changed (95
distinct texts: 120 reads, plus `ledger.create_action` gaining `job $4`), and every old check
is a subset of its new header. New row kinds are `job`, `job_subject` and `collection`. The
build fails a read of an unlisted table, or by an id-named column with no kind, unless
`[not_ids]` exempts that column with a reason. Three columns are exempt, all YouTube or
provider ids. The read routes keep a first-party job to its library (and a first-party video
job to its video). `data/*` and `exports/read` check their subject, and the worker's relayed
`job_ref` answers about the job's own id only. Merged at 0770a41 (head 9c39f33).

## What we learned
- `processing_jobs.subject_id` is polymorphic (`subject_kind = $1 AND subject_id = $2`). One
  kind, `job_subject`, resolves it through the tables that own each subject kind. It reads the
  job rows only when none of those has the id: `dup_group` jobs have no foreign key, so a
  deleted group's jobs outlive it. Only job rows with a library count, because `checkRow`
  allows as soon as a row has no library, and one such row would open the subject to everyone.
  The commit security review found these gaps in three passes.
- Checking the whole tree, not only top-level AND terms, refused no first-party call. Parity
  (211 local, 256 in CI) passed unchanged with strict video-job `subject` checks on reads.
- A scratch cluster from `initdb` without `-E UTF8` is SQL_ASCII, and so is `template1`, which
  the test harness copies for each database. Livetv parity then fails with
  `22P05 unsupported Unicode escape sequence`, which looks like a code fault.
- The rehearsal's scenario 12 went red on main once the previous release became v0.3.106,
  because it expected the removed `ext.dizquetv` row. #1032 fixed it. A duplicate fix opened
  here was closed in its favour.

## Keep doing
- Prove a new check refuses and allows over the whole embedded bundle: the walk now covers
  reads, requiring at least 200 read checks.
- Treat each automated security-review finding on a resolver as a lead, and check it against
  the schema (foreign keys, check constraints) before keeping or dropping a branch.

## Stop doing
- Bringing main in with a plain `git merge origin/main`. Use `tools/dev/merge-main.sh` even
  when no conflict is expected. One plain merge here was reset before it was pushed.

## Open
- #998 (wave 9f, core-media) needs `statements_build.py sync core-media` again: its reads
  gain checks, and any table it reads by id that the registry lacks fails `check`.
- The `job` kind lets any job read a catalog job's row (`library_id` NULL, no library's). That
  is harmless today: flags and hashes only.
- The worker's `publish` relay op still takes any payload (from #1005's list).
