# Story 54.36 — Wave 9: the core stages, and removing the in-process path

> Epic 54 · Phase 4 · Depends on: 54.35 (§4 also on 54.43) · Needed by: —

## Why

The last wave moves the stages every home always runs (scan, probe and the identity stages),
then deletes what made the migration safe: the in-process handlers, `CORE_MODULES` and the
switch. After it, the pipeline worker is the queue engine plus dispatch, and every stage is a
plugin (README §15.3, F19).

## Description

### 1. The stages

All in **`tv.maktaba.core-media`**, the plugin of the `core.pipeline` component (kind
`stages+process+loop`, `switchable = false`, 54.27 §3), runner group `light` (the
`plugin-runner/light` instance of `core.plugin_runners`, 54.23). `component_catchup` stays in
`core.pipeline` as an engine stage; the main worker (`pipeline/`), `pipeline/chroma-copy` and
the pipeline loops stay too:

| Stage | Today | Pool | Trusted needs |
|---|---|---|---|
| `scan` (legacy) | `handlers.py:scan_handler`, `scanner/` | `nas_read` | `files.read:library` |
| `probe` (legacy) | `handlers.py:probe_handler` | `nas_read` | `files.read:library`, `exec:ffprobe` |
| `content_hash` | `stages/content_hash.py`, `identity/` | `nas_read` | `files.read:library` |
| `thumb_hash` | `stages/thumb_hash.py` | `nas_read` | `files.read:library`, `exec:ffmpeg` |
| `link` | `stages/link.py` | `cpu` | — |
| `fingerprint` | `stages/fingerprint.py`, `audio/chromaprint` | `nas_read` | `files.read:library`, `exec:fpcalc`, `exec:ffmpeg` |
| `style_sample` | `stages/style_sample.py` | `nas_read` | `files.read:library`, `exec:ffmpeg` |
| `fp_gate` | `stages/fp_gate.py` | `cpu` | — |
| `access_check` | `stages/access_check.py` | `nas_read` | `files.read:library` |
| `storage_canonicalize` | `stages/storage_canonicalize.py`, `sharepath/` | `cpu` | — |
| `categorize` (legacy) | the categorize handler | `cpu` | — |

### 2. Legacy first

`scan`, `probe` and `categorize` stop chaining by hand: their enqueues become declared edges,
and `advance_after_stage` (core) keeps writing `videos.state` from their results, as it does for
every engine stage. Each conversion lands in its own PR, in-process, green in parity against
the old behaviour, before the move.

### 3. The watcher stays

The filesystem watcher (`watcher/`) and manual scans are loops in the core that **enqueue**
`scan`; they do not move. `scanner/`'s walking code moves with `scan`.

### 4. Removing the in-process path

§4 does not start one release after these stages flip. It waits until **all** of (a) to (e)
hold, each checked again in the code and on a live home right before it starts. Until then,
the in-process code stays, and so do the per-stage switch, the flip route
(`PUT /api/queues/{stage}/impl`) and the CLI (`maktaba-server stages impl`): the switch is an
admin's only lever when a plugin stage misbehaves, and its target, the in-process code, stays
anyway. The release after wave 9 (v0.3.109, PR #998) checked them on 2026-11-09: no stage of
any wave was flipped back (every `job_stages` row has an empty `impl_changed_by`), and none of
(a) to (e) held.

**(a) Every stage of `GRAPH` has a plugin, or is kept in the core on purpose.** Kept in the
core: `component_catchup` (an engine stage of `core.pipeline`, §1). The stages still
`inproc` on 2026-11-09, and what each waits on:

| Stage | Where it is | Waits on |
|---|---|---|
| `component_catchup` | `engine/stages/component_catchup.py` | nothing: kept in the core |
| `enrich` | `engine/stages/enrich.py` | an owner decision (54.31: it reads any page a search result links to, which no declared host list covers) |
| `categorize` | `GRAPH` (legacy, no handler) | an owner decision: removed, or kept declared |
| `word_check` | `engine/stages/word_check.py` | conversion into a first-party plugin |
| `display_title` | `engine/stages/display_title.py` | conversion into `tv.maktaba.classify` (it asks the language model, 54.31) |
| `youtube_collections` | `engine/stages/youtube_collections.py` | conversion (it builds series from the library as a whole) |
| `youtube_tartube` | `engine/stages/youtube_tartube.py` | conversion (the Tartube import, likewise) |
| `dizquetv_audit`, `dizquetv_channels`, `dizquetv_show` | nowhere: retired (`engine/retired.py`, `shared/components/retired.go`); their code went with the dizqueTV extension, slot 0215 kept their rows | a cleanup migration that deletes their `job_stages`, `processing_jobs`, `worker_pools` and component rows (with the retired Plex stages' rows); they need no plugin |

**(b) A SQLite home has a path that is not in-process, or SQLite homes have an announced end
of support.** SQLite is the default database of a fresh single-host install
(`config.Defaults`), and on SQLite the worker runs every first-party stage in-process
(`plugins/native.py`, `choose`: the plugin API and its per-plugin roles are Postgres only).

**(c) The Windows share fallback is retired.** On Windows, while a library is on a share, a
library stage that the Windows end to end has not proven in its runner runs in the worker
(`windows_share_library`, after v0.3.105). Retiring it needs share access in the runner's
sandbox, proven by that end to end for every library stage.

**(d) The Chroma index store is retired, or `index` and `chapter_infer` run over it from the
runner.** While the search index is Chroma (a folder one process may open), the two stay in
the worker beside the query server (`index_store_chroma`).

**(e) The in-process fallbacks are counted where a person can read them** (story 54.43): each
machine's count per stage and reason is kept in the database and shown to admins, so "this
home never falls back" is a reading, not an assumption. The reading must show no fallback on
the live home for a full release before §4 starts.

Once (a) to (e) hold, §4 is:

1. delete `engine/stages/` modules, `handlers.py`'s stage handlers and `CORE_MODULES`
   (except the stages kept in the core);
2. delete `GRAPH`'s remaining moved `StageDef`s and keep `graph.py` as types, pools and the
   stages kept in the core;
3. drop `job_stages.impl` with the flip route and the CLI (54.25): one release stops using
   the column, and the contract migration that drops it ships in the release after
   (Epic 35's rule), taking its migration slot at merge;
4. the import contract (54.27) now forbids `maktaba_pipeline` from importing any plugin, and
   the pipeline wheel no longer depends on the ML extras;
5. `graph-golden` stays: the assembled graph must still equal the golden file.

A stage flipped back by the owner blocks step 1 for that stage's code only; the release notes
say which and why. The update rehearsal's rows that flip `impl` (16.x, 16.7 among them) are
rewritten in step 3's first release to prove what they still can across releases.

## Acceptance criteria

- **Given** the parity fixture,
  **when** `plugins-parity` runs the eleven stages,
  **then** rows (including `videos.state` written by the engine), results, fingerprints and
  enqueued jobs are identical, and N16 holds.
- **Given** a new file dropped into a library,
  **when** the watcher sees it,
  **then** `scan` and `probe` run in the `light` runner and the video reaches the same state as
  before, with the same downstream jobs.
- **Given** the removal release,
  **when** the pipeline wheel is built,
  **then** it contains no stage code, imports no plugin, and every stage of
  `graph.golden.json` is served by a first-party plugin or is a stage kept in the core.
- **Given** the `core-media` plugin,
  **when** an admin tries to disable it,
  **then** `409 component_not_switchable`, as `core.pipeline` answers today.
- **Given** an update mid-scan and a rollback,
  **when** they finish,
  **then** no job is lost or rerun, and the library's states are unchanged.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | parity | fixture | eleven stages | identical; N16 |
| T02 | parity | legacy `scan`, `probe`, `categorize` before and after §2, in-process | run | same jobs and states |
| T03 | e2e (`plugins-e2e`) | real watcher, runner, API | drop a file | same states and downstream jobs as before |
| T04 | CI | removal release | wheel content, import contract, golden graph | no stage code; all stages served |
| T05 | role test (go) | `PUT /api/components/core.pipeline` | admin | `409 not_switchable` |
| T06 | update rehearsal scenario 16.9 | mid-scan | update and rollback | as AC |
| T07 | unit (`fp-core-media`) | moved tests | run | green |

## Edge cases

- **A home that never upgraded past an earlier wave** updates straight to the removal release:
  Epic 35's update path walks releases in order where a release says it must not be skipped;
  every wave's release is marked so, so a home never lands on a release whose in-process code
  is gone while its stages were never flipped.
- **The light runner fails to start on a home** (a broken sandbox on an old kernel): before the
  removal release, the stages fall back to `inproc` automatically and the Components page says
  why; after it, `core.plugin_runners` is *Failed* with the runner's reason
  (`sandbox_unavailable` or `runner_crash_loop`) and `core.pipeline` reads *Degraded*, and the
  updater's pre-check (Epic 35, beside 51.18's packs step) refuses to install the removal
  release on a home whose runner check fails.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `plugins/first-party/core-media/` (new) | §1–§3; `scanner/`, `identity/`, `audio/` fingerprint parts, `sharepath/` stage parts move. |
| `pipeline/src/maktaba_pipeline/handlers.py`, `engine/stages/`, `engine/graph.py`, `engine/stages/registry.py` | §2, then §4 removal. |
| `shared/db/migrations/` | Role; `impl = plugin` for the eleven; later the contract migration dropping `impl`. |
| `api/internal/handlers/queues/impl.go`, CLI | Removed in §4. |
| `cmd/maktaba-server/internal/updater/` | Wave releases marked "do not skip"; runner pre-check. |
| `tests/update_rehearsal/scenario_16_9_wave9.*` | T06. |

### Tests

- Parity T01, T02; e2e T03; CI T04; role T05; rehearsal T06; unit T07.

### Rollout

The release flips the eleven at update. The removal (§4) ships once §4's (a) to (e) hold.

### Rollback

Before §4: per stage switch and Epic 51 rollback. After §4: Epic 51 release rollback to the
release that still has the in-process code.

### Dependencies

54.35 (every other stage already a plugin for a release).

## Built (2026-11-07)

§1 moved `scan`, `probe`, `content_hash`, `thumb_hash`, `link`, `fingerprint`,
`style_sample`, `fp_gate`, `access_check` and `storage_canonicalize` into
`tv.maktaba.core-media`, in the `light` runner (its role in slot 0214, which also lets
`tv.maktaba.thumbnails` write `videos` for `thumbnail`). `thumbnail` joined
`tv.maktaba.thumbnails` and `extract` joined `tv.maktaba.transcription`, the plugins of their
components (README §15.2, §15.12). `categorize` stays in the core graph as it is, pending a
decision on its future; `component_catchup`, the watcher and the manual scans stay too. The
writes only the queue engine holds are relayed to the worker: a video's state (`advance`),
a failed stage queued again (`requeue_failed`) and the path conversion
(`storage_canonicalize`), each checked against the job's stage, video or library. The flip at
update (the graph sync) sets the moved stages to the plugin on Postgres. Parity (T01) scans a
library of real files, probes it, links a video from its yt-dlp sidecar and runs each video's
identity, fingerprint, style sample and access check: rows, `videos.state` included, the
queue and the job log are identical on both sides, and N16 holds. T05: every write to
`core.pipeline` is `409 not_switchable`. T06 waits for the update rehearsal's earlier
scenarios (16.1 to 16.8, issue #804). §4 (removing the in-process path) waits for its preconditions (a) to (e), checked on
2026-11-09 after v0.3.109 to v0.3.112 shipped: none held yet, and no stage was flipped back.
