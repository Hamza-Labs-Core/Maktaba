# Epic 40 — Queue health: a red tile means something is wrong

> **Status:** spec. **Source:** `specs/epics/40-queue-health/`.
> **Anchors:** [Epic 6 — job queue](../06-job-queue/README.md) (plan 06-11: `processing_jobs`, the
> claim, `mark_failed_or_retry`, `error_cause` §6.3, the issues panel §8), `web/src/pages/queue/*`
> (the queues page), `api/internal/handlers/queues/*` (its API), `pipeline/src/maktaba_pipeline/engine/*`
> (the engine, `causes.py`, `hooks.py`, `reconcile.py`), `db/jobs_state.py` (retry and backoff),
> the `repair_broken` stage (plan 06-11 mediaintel parity, slot 0112), the share paths of
> [story 31.3](../31-multi-instance/README.md) (`sharepath/resolver.py`, `instance_identity.path_map`).

## Goal

The owner's decisions (2026-10-21):

1. **A tile's colour says what to do.** A stage is not red because a small share of its jobs
   failed. Red means *many recent jobs fail the same new way* (a bug or a bad setting). Jobs
   waiting on something outside them (the model, the share, disk space) are **Waiting**, and resume
   on their own. Items that no retry can fix (a missing or damaged file) are a **Needs you** count,
   not a red tile.
2. **Every failure has a class**, decided by data, not by `if`s spread through the stages:
   `transient`, `dependency:<name>`, `resource:<name>`, `item_permanent`, `unknown`.
3. **Parked jobs never burn attempts.** Small health probes watch the dependencies; when one is
   healthy again, the jobs parked on it are queued again by themselves.
4. **One page lists every failing item** — all stages, all classes, waiting ones too — with filters
   and the fixes an admin can make there (fix path, check permission, retry now, ignore, re-download,
   rescan, remove), one item or a whole group at a time, every action audited.
5. **The failures there today are moved into this model** (the 6,577 `check_tags` and 1,029
   `check_clips` model failures become Waiting, missing files go to Needs you).

## Why (live data, 2026-10-21)

| Stage | Failed | `error.kind` / cause | What it really is |
|---|---:|---|---|
| `check_tags` | 6,577 | `model_unavailable` | the LLM was not reachable; nothing wrong with the items |
| `check_clips` | 1,029 | `model_unavailable` | same |
| `content_hash` | 853 of 149k (0.6%) | `file_missing` | file gone from the NAS share (or the share was offline) |
| `fingerprint` | 438 of 30k | `import` "read failed: N frames" | a partial read; `retryable` but attempts stop at 1 |
| `chapter_thumbs` / `thumbnail` | 384 / 83 | ffmpeg rc | likely damaged files |
| `clip_groups` | 24 | `DiskFullError` "could not resize shared memory segment" | Postgres shared memory: a resource, not the items |
| `plan_channels` | 1 | — | — |

Every one of these paints its tile red today (`queueVisualState`: `failed_open > 0` → `failing`),
so the page is red all the time and a real breakage cannot be seen.

How it works today (main, 3fd44a97):

- A stage raises `StageFailure(kind, message, retryable)`; any other exception becomes
  `kind = <ExceptionName>`, `retryable = True`. `mark_failed_or_retry` (`db/jobs_state.py`) retries
  with backoff (`pipeline/backoff.py`, 60 s doubling to 1 h) while `retryable and attempts <
  max_attempts`, else the row is `failed`. The claim adds one to `attempts` (not when resuming a
  `paused` row). `error` holds `{kind, message, traceback, retryable}`.
- `engine/hooks.on_failure` stores `error_cause` = `causes.classify(kind, message)`: a known code
  (`file_missing`, `nas_timeout`, `plex_unreachable` …) or `kind:<normalised message>`.
- The API groups open failures by `(stage, error_cause)` over each subject's latest row
  (`failures.go`); `QueueSummary.counts.failed_open` drives the red tile.
- An operator retry enqueues a new forced row (`control.go` `Retry`); the failed row stays history.
- `repair_broken` downloads again, from YouTube, files whose `probe` failed (off until
  `rules.repair.enabled`), through `library_mgmt.repair.fetch_and_swap`.

## Model

### Failure classes

| Class | Meaning | On failure |
|---|---|---|
| `transient` | a passing problem (a NAS timeout, a partial read, a dropped DB connection, an ffmpeg failure that may be the share) | retry with the usual backoff while `attempts < budget`; budget = `max(max_attempts, rules.queue_health.transient_attempts)` (a stage with `max_attempts = 1` runs once by design and keeps 1); then `failed` |
| `dependency:<name>` | something the job needs is down: `dependency:model:<rules section>` (the LLM of a stage with `model_rules`), `dependency:share:<library root>` | **parked** (below); no attempt spent. If the dependency's last probe says healthy, the dependency is not the cause: the failure is treated as `unknown` (no endless park/release loop) |
| `resource:<name>` | the machine ran out of something: `resource:disk`, `resource:shm` | parked until the resource frees; no attempt spent |
| `item_permanent` | retrying cannot help: a missing file (once the share is known to be up), a damaged file, no permission, a video gone from YouTube | `failed` at once → **Needs you** |
| `unknown` | nothing above knows it | retry within the stage's own `max_attempts`, then `failed`. On the read side it is grouped by signature: a few items are item-specific (Needs you, "unexplained error"); many in the window make the stage **Failing** |

A stage that says `retryable=False` is never retried, whatever the class (only parking overrides
it: a parked job has not really been tried).

**The mapping is data** (`engine/failure_classes.py`): an ordered table of `(signature prefix →
class)` over the normalised `error_cause` (so unknown kinds are matched by their normalised message
signature), checked after the admin's own rows (`rules.queue_health.class_overrides`). Defaults:

| Signature (`error_cause`) | Class |
|---|---|
| `model_unavailable` | `dependency:model:<stage.model_rules>` |
| `file_missing` | `dependency:share:<root>` when the item's library root does not answer, else `item_permanent` |
| `shm_full` (new cause: "could not resize shared memory segment") | `resource:shm` |
| `disk_full` (new cause: "no space left on device", `ENOSPC`, `DiskFullError` without shm) | `resource:disk` |
| `nas_timeout`, `nas_partial_read`, `nas_invalid_argument`, `db_error`, `import:read failed`, `ffmpeg_rc` (new cause: ffmpeg exit codes of the thumbnail stages) | `transient` |
| `ffmpeg_decode`, `permission_denied` (new cause), `youtube_unavailable` | `item_permanent` |
| anything else | `unknown` |

`causes.py` gains the codes `shm_full`, `disk_full`, `permission_denied`, `ffmpeg_rc` (patterns
placed before `db_error` / the generic ones, so a Postgres `DiskFullError` is not a `db_error`).

### Parking

A parked job is a **live `pending` row** with `parked_on = '<class>'` and `not_before = now +
rules.queue_health.park_max_hours` (a backstop: it is tried again then even if no watcher released
it). Parking refunds the attempt the claim took (`attempts - 1`, never below 0), records
`failure_class`, `error`, `error_cause`, and logs a `parked` job event. The claim clears `parked_on`.
Releasing = `parked_on = NULL, not_before = now`.

### Dependency watchers

One loop on the leader worker (advisory lock `maktaba.queue_health`, like the reconciler), every
`probe_interval_seconds`, writes `dependency_health(name, healthy, message, checked_at, since)`:

- `dependency:model:<section>` — for each rules section named by a stage's `model_rules`:
  `GET <url>/api/tags` (Ollama) within `probe_timeout_seconds`; healthy when it answers 200 and, if
  it lists models, lists `cfg.model`.
- `dependency:share:<root>` — every library root (through this machine's path map): the folder
  exists and can be listed.
- `resource:disk` — free space of the temp and data folders ≥ `min_free_disk_gb`.
- `resource:shm` — Postgres' shared memory cannot be measured from a worker: healthy again
  `resource_cooldown_minutes` after the last `shm_full` failure.

When a dependency is healthy, every job parked on it is released (in batches of 1,000), and a
`dependency.healthy` job event says how many. A node whose mount is down while the home's is up
parks again on its own failure and spends nothing.

### Missing files and the scan

A `file_missing` with the share up is `item_permanent`, shown as *missing — the next scan will
confirm*. After every completed scan of a library whose roots all answered, the scanner checks each
video with an open `file_missing` failure: the file is there again → `videos.missing_confirmed_at =
NULL` and the video's failed jobs are queued again; still gone → `missing_confirmed_at = now` (*missing
— confirmed by the scan of <date>*), with **Re-download** offered when it has a YouTube id.

### Tile states (read side)

Over the last `window_hours` (from `job_events` kinds `failed` / `retry_scheduled` / `parked`, whose
`data` carries `cause` and `class`, and the stage's done rows):

1. **Failing** (red) — `failure_share ≥ ok_failure_share` **and** one signature of class `unknown`
   or `transient` has `≥ failing_min_jobs` failures in the window. The tile shows the signature, its
   count and one example message and item.
2. **Waiting** (amber) — jobs are parked: the tile says on what ("the model at
   http://…:11434 is not answering since 10:42", "share smb://nas/Originals is offline", "low disk
   space") and how many, and that they resume on their own.
3. Otherwise the tile keeps today's running / waiting / done look (**OK**, neutral); the failed
   count shows as small text.

**Needs you** is a count badge on any tile: open failed items (the latest row of a subject and
stage is `failed`), not ignored, not part of the stage's Failing signature. The tile's failed count
and the badge link to the failures page, filtered to the stage.

## The failures page (`/queue/failures`)

One list of **every failing item**, all stages and classes: the latest row of a subject and stage
that is `failed`, parked, or pending a retry after a failure.

- **Filters:** stage, class, signature, library, path prefix, status (Needs you / Failing / Waiting
  / Retrying / Ignored). The filters live in the URL, so a tile links to `?stage=content_hash` and
  the badge to `?stage=content_hash&status=needs_you`.
- **Columns:** item (title) and file path, stage, error message and class, the Needs-you reason,
  attempts / budget, first and last failure, library, last access check.
- **Groups:** a signature header with its count selects the whole group (server-side, by filter,
  up to 20,000 items).
- **Actions** (admin, one item, a selection or a whole filter; each answers how many it touched):
  - **Retry now** — failed: a new forced row (attempts start at 0); parked or backing off: released now.
  - **Fix path** — one video: set its stored path, then retry. Or **add a path-map rule** (`share
    prefix → local mount`) merged into the home's override (`instance_identity.path_map`, the one the
    Machines page edits; the workers read it as they do today), then retry the selection.
  - **Check access** — shows the stored permission error; queues an `access_check` job per video
    that, on the worker host, opens the file and reads 64 KiB, records `item_checks(ok, error, host,
    checked_at)` and, when it reads, queues the video's failed jobs again. The page explains what to
    change on the NAS when it does not (Maktaba cannot change share permissions).
  - **Ignore / Unignore** — `failure_ignores(subject, stage)`: the item leaves every count, tile and
    badge; the *Ignored* status filter brings it back. A later failure of the same item and stage
    stays ignored until unignored.
  - **Re-download** (videos with a YouTube id) — an `item_requests` row (`redownload`) and a forced
    `repair_broken` job for the library; `repair_broken` fetches requested files even when automatic
    repair (`rules.repair.enabled`) is off, through the same `fetch_and_swap`, then asks `probe` and
    `content_hash` again.
  - **Rescan** — a `scan` job for each library of the selection.
  - **Remove** — the video leaves the library (as `DELETE /api/videos/{id}`: the row, not the file),
    its live jobs are cancelled. A file still on the share comes back at the next scan: ignore it
    instead to keep it out of sight.
- **Audit:** every action writes `audit_log` (category `admin`, action `queue.failures.<action>`,
  actor, the filter or ids, the count) and goes through the API with admin auth (403 otherwise).

## Rules (`rules.queue_health`, section "Queue health")

| Rule | Default | Help |
|---|---:|---|
| `window_hours` | 6 | How far back a tile looks to decide its colour. |
| `ok_failure_share` | 0.02 | Below this share of failed jobs in the window a stage is never red; its failed count shows as small text. |
| `failing_min_jobs` | 10 | A stage turns red only when at least this many jobs failed the same new way in the window. |
| `transient_attempts` | 5 | How many times a passing failure (a NAS timeout, a partial read) is tried before the item needs you. |
| `park_max_hours` | 24 | A job waiting on the model, the share or disk space is tried again after this even if no probe released it. |
| `probe_interval_seconds` | 60 | How often the model, the shares and disk space are checked. |
| `probe_timeout_seconds` | 5 | How long a check waits for an answer. |
| `min_free_disk_gb` | 5 | Below this much free space jobs that need disk wait. |
| `resource_cooldown_minutes` | 10 | How long jobs that ran out of Postgres shared memory wait before they are tried again. |
| `class_overrides` | — | Rows `(signature, class)`: a failure whose signature starts with this is of this class (`transient`, `item_permanent`, `unknown`, `dependency:…`, `resource:…`). Checked before the built-in table. |

`api/internal/handlers/rules/schema.json` is regenerated (`python -m maktaba_pipeline.rules
--schema …`); the Go API reads the same section for the tile thresholds.

## Schema (slot `0143_queue_health`, with `.sqlite.sql`)

- `processing_jobs.failure_class text`, `processing_jobs.parked_on text` (+ partial index on
  `(parked_on) WHERE parked_on IS NOT NULL`).
- `dependency_health(name text PK, healthy bool, message text, checked_at, since)`.
- `failure_ignores(subject_kind, subject_id, stage, ignored_by, ignored_at, PK(subject_kind,
  subject_id, stage))`.
- `item_requests(id, video_id → videos ON DELETE CASCADE, action, requested_by, requested_at,
  done_at, result)`.
- `item_checks(video_id PK → videos ON DELETE CASCADE, ok, error, host, checked_at)`.
- `videos.missing_confirmed_at`.
- `job_stages` row `access_check` (video subject, `nas_read` pool).
- `maktaba_worker`: SELECT/INSERT/UPDATE on `dependency_health`, `item_checks`; SELECT/UPDATE on
  `item_requests`; SELECT on `failure_ignores`.

Additive; no data changed by the migration (the existing failures are moved by 40.6).

## Stories

| # | Story | Scope |
|---|---|---|
| 40.1 | **Failure classes and parking** | migration 0143; `causes.py` new codes; `engine/failure_classes.py` (table + overrides); `rules.queue_health` + schema.json; `mark_failed_or_retry` decides by class on an engine connection (park / retry within the class budget / fail); `on_failure` records class and logs `parked`; the claim clears `parked_on`; job events carry `class`. **AC:** a `model_unavailable` failure with the model unhealthy parks with attempts unchanged; with the model healthy it retries as `unknown`; `nas_timeout` retries up to 5; `ffmpeg_decode` fails at once; an override row changes a class; SQLite and Postgres. |
| 40.2 | **Dependency watchers** | the leader loop: model, share and disk probes, the shm cooldown, `dependency_health`, release of parked jobs when healthy, the `park_max_hours` backstop. **AC:** a parked job is released within one interval of its dependency turning healthy; a down dependency releases nothing; only one worker probes. |
| 40.3 | **Missing files at the scan, access checks, requested re-downloads** | `file_missing` → `dependency:share:<root>` when the root is down; the scanner's post-scan check (heal and requeue, or confirm); the `access_check` stage; `repair_broken` takes `item_requests` (`redownload`) even when automatic repair is off. **AC:** a file back on the share is requeued by the next scan; a confirmed one gets `missing_confirmed_at`; a readable access check requeues the video's failures. |
| 40.4 | **API: tile health and the failures list** | `QueueSummary.health {state, failed_window, finished_window, failure_share, waiting[], needs_you, failing?}`; `GET /api/queues/failures` (filters, keyset paging, facets); `POST /api/queues/failures/actions` (`retry`, `ignore`, `unignore`, `rescan`, `remove`, `redownload`, `fix_path`, `path_map`, `check_access`) with audit; ignored items leave every count. **AC:** handler tests per action (admin only, audited, counts), a red tile only for a dominant signature over the share. |
| 40.5 | **Web: tiles and the failures page** | `QueueCard` states from `health` (Failing, Waiting, OK) with the Needs-you badge and links; `/queue/failures` with URL filters, groups, selection, bulk and per-item actions, the fix-path / path-map / access dialogs, the Ignored filter; en + ar strings. **AC:** vitest per state and action, a11y. |
| 40.6 | **Move today's failures** | a one-time backfill on the leader (`app_settings` key `queue_health.backfill`): every open failed row without a class is classified; dependency/resource classes get a new parked row (released at once by the watcher if the dependency is up), transient ones under budget are queued again, the rest keep their failed row with the class (Needs you). **AC:** model failures become Waiting, missing files Needs you, fingerprint partial reads retried; idempotent. |

## Order

40.1 → 40.2 → (40.3, 40.6) → 40.4 → 40.5. 40.1–40.3 and 40.6 are pipeline and migration work
(one or two PRs); 40.4 is Go; 40.5 is web. Each PR goes through the usual flow (tests, CI green,
review, merge); the migration is applied to the live database by the main session.

## Out of scope

- Probing Plex, dizqueTV and YouTube as dependencies (their failures stay `transient` /
  `service_retrying` as today).
- Changing permissions on the NAS (the access check tests and explains; the admin changes the share).
- Deleting files from the share (Remove forgets the item; the file stays).
