# Story 54.43 — Counting the in-process fallbacks

> Epic 54 · Phase 4 · Depends on: 54.25 · Needed by: 54.36 §4

## Why

A first-party stage whose `job_stages.impl` is `plugin` still runs in the pipeline worker when
its runner cannot take the job on this machine: the runner is not serving here, its role or
sandbox is stale, the library is on a Windows share, the search index is Chroma, the plugin
ran a statement it does not have, or its sandbox refused a file. On SQLite every first-party
stage runs in the worker. The worker counts these in memory only (`plugins/native.py`,
`FALLBACKS`), so the count is lost at every restart and no person can read it. Removing the
in-process path (54.36 §4, precondition (e)) needs "this home never falls back" to be a
reading, not an assumption.

## Description

### 1. The count

Migration: a table `stage_fallbacks` (Postgres and SQLite):

| Column | Type | Meaning |
|---|---|---|
| `machine` | text | the worker's machine: its cluster node id, or `home` for the home's own worker |
| `stage` | text | the stage |
| `reason` | text | why the job ran in the worker (below) |
| `count` | bigint | jobs (or fingerprints) that fell back, since the row was made |
| `first_at`, `last_at` | timestamp | the first and the latest fallback counted |

Primary key `(machine, stage, reason)`. Expand-only: older releases ignore the table.

The reasons are the worker's own, unchanged: `runner_not_serving_here`,
`windows_share_library`, `role_unusable`, `index_store_chroma`, `runner_areas_stale`,
`unregistered`, `sandbox_refused`, plus two that were not counted before:
`fingerprint_inproc` (the `fp` runner did not answer, so the in-process fingerprint ran) and
`sqlite` (a SQLite database: every first-party stage runs in the worker). A stage an admin set
to `inproc` is not a fallback and is not counted.

### 2. Writing it

The worker keeps adding to its in-memory counts as it does now, and at each 30 s refresh
(`WorkerAgent.refresh`) and when it stops, it adds what it counted since the last write to the
table in one statement per row (`INSERT … ON CONFLICT DO UPDATE SET count = count + excluded`),
then clears what it wrote. A write that fails keeps the counts for the next refresh; it never
stops a job or the refresh.

### 3. Reading it

`GET /api/queues/fallbacks` (admin only, like every queue route): every row, newest
`last_at` first, with the machine's name (the node's name, or the home's) and the total. An
optional `?stage=` narrows to one stage. The Queue detail page shows the stage's rows under
"Ran in the worker instead" (reason, machine, count, last time), or "No in-process fallbacks
counted", with every string and reason label in English and Arabic.

## Acceptance criteria

- **Given** a stage at `plugin` whose runner is not serving,
  **when** three of its jobs run in the worker and the worker refreshes,
  **then** `stage_fallbacks` has `(home, stage, runner_not_serving_here, 3)`, and after two
  more and a restart of the worker, `5`.
- **Given** the database cannot be written at a refresh,
  **when** the next refresh succeeds,
  **then** the counts of both refreshes are in the table, and no job failed.
- **Given** an admin,
  **when** they call `GET /api/queues/fallbacks`,
  **then** 200 with each row, its machine's name and the total.
- **Given** no one signed in, a viewer or a cloud-linked play sign-in,
  **when** they call `GET /api/queues/fallbacks`,
  **then** 403 before any query runs, as every queue route answers.
- **Given** the Queue detail page of a stage with counted fallbacks,
  **when** an admin opens it in English or Arabic,
  **then** each reason, machine, count and last time is shown in that language.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (py) | counts in memory | flush to SQLite | rows added; counts cleared; a second flush adds to them |
| T02 | unit (py) | a flush that raises | flush again | both counts written; nothing raised |
| T03 | unit (py) | `fp` runner down | fingerprint | `fingerprint_inproc` counted |
| T04 | integration (PG, go) | rows | `GET /api/queues/fallbacks`, `?stage=` | rows, names, total; narrowed |
| T05 | role test (go) | the route | no one / viewer / cloud-linked play / admin | 403 / 403 / 403 / 200 |
| T06 | unit (web) | Queue detail | render with and without rows | rows and the empty line, en + ar |
| T07 | migration (`RollbackSafe`) | the slot | up, down, up | clean |

## Edge cases

- **A joined machine on an older release** writes nothing; the home shows only the machines
  that count.
- **A worker killed without stopping** loses at most 30 s of counts.
- **A retired stage** (`engine/retired.py`) is left out of the reading, as every queue reader
  does.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/db/migrations/NNNN_stage_fallbacks.sql` + `.sqlite.sql` | §1 (slot taken at merge; `SchemaRev` in its three places). |
| `pipeline/src/maktaba_pipeline/plugins/native.py`, `engine/run.py` | the two new reasons; the flush. |
| `pipeline/src/maktaba_pipeline/cluster/node.py` | flush at each refresh and at stop. |
| `api/internal/handlers/queues/fallbacks.go` (new) | §3, the route. |
| `web/src/pages/queue/QueueDetail.tsx`, `web/src/lib/queueApi.ts`, `web/src/i18n/{en,ar}.json` | §3, the section. |
| OpenAPI, the devportal's queue reference | the route. |

### Tests

- Unit T01–T03, T06; integration T04; role T05; migration T07.

### Rollout

Ships with the table empty; counts start with the release. 54.36 §4 (e) asks for a full
release with no fallback on the live home.

### Rollback

Release rollback; the Down script drops the table.

### Dependencies

54.25 (the switch and `native.choose`).
