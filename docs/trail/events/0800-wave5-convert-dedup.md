---
schema: 1
kind: pr
pr: 800
title: Converting, duplicates and the file trash run as Maktaba's built-in plugins (Epic 54 wave 5, 54.32)
date: 2026-11-07
session: 8687f2a4-808d-47dd-93b7-2c1167e0f385
prs: [789]
---

## What happened
- `convert_mp4` moved into `tv.maktaba.convert`; `dedup_scan`, `dedup_apply`, `trash_purge` and `file_restore` moved into `tv.maktaba.dedup`. Both run in the `light` runner and write only the library and its trash. Roles: migration 0205 (0203 and 0204 are the store's).
- What only the engine may write, the worker writes for a plugin's job after checking it against the job: `enqueue`, a duplicate group's merge (`dedup_commit`) and a restored video's state (`unsupersede`).
- `library_mgmt`'s run-time SQL (for SQLite's lack of arrays) became one fixed text per dialect.

## What we learned
- A duplicate merge re-points every table that references a video, from the catalogue. No plugin role can hold that without most of the schema, so it belongs to the engine. Put the whole transaction behind one relayed op, checked against the job's group, rather than granting the tables.
- `may_request` is a graph rule, not a permission list: adding `convert_mp4` to `dedup_scan` made a request cycle the graph refuses. A relay rule that allows graph neighbours keeps the in-process behaviour.
- `$1 IS NULL` does not type a parameter on Postgres (`AmbiguousParameterError`). An optional filter needs a cast per dialect.
- Ids made by Python's `uuid` module, as the ledger and duplicate groups make them, can be given to both parity sides by replacing that module's name with one whose `uuid4` is `SameIds`.
- `migration-lint` refuses a slot gap that is not listed as permanent. A PR that takes a slot after two open PRs' slots is red until they land.

## Open
- The job-scoped path check of 54.32 §2 (T02): issue #796.
- Wave 4 (`display_title`, `compute.generate`) is another session's.
