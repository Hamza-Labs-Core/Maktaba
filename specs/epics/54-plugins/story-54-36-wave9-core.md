# Story 54.36 — Wave 9: the core stages, and removing the in-process path

> Epic 54 · Phase 4 · Depends on: 54.35 · Needed by: —

## Why

The last wave moves the stages every home always runs (scan, probe and the identity stages),
then deletes what made the migration safe: the in-process handlers, `CORE_MODULES` and the
switch. After it, the pipeline worker is the queue engine plus dispatch, and every stage is a
plugin (README §15.3, F19).

## Description

### 1. The stages

All in **`tv.maktaba.core-media`** (`switchable = false`, the `core.pipeline` component),
runner group `light`:

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

One release after these stages flip (and only if no stage of any wave is flipped back):

1. delete `engine/stages/` modules, `handlers.py`'s stage handlers and `CORE_MODULES`;
2. delete `GRAPH`'s remaining `StageDef`s (none left) and keep `graph.py` as types and pools;
3. drop `job_stages.impl` in a contract migration, and the flip route and CLI (54.25);
4. the import contract (54.27) now forbids `maktaba_pipeline` from importing any plugin, and
   the pipeline wheel no longer depends on the ML extras;
5. `graph-golden` stays: the assembled graph must still equal the golden file.

A stage flipped back by the owner blocks step 1 for that stage's code only; the release notes
say which and why.

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
  `graph.golden.json` is served by a first-party plugin.
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
| T05 | role test (go) | `PUT /api/components/core.pipeline` | admin | 409 |
| T06 | update rehearsal scenario 16.9 | mid-scan | update and rollback | as AC |
| T07 | unit (`fp-core-media`) | moved tests | run | green |

## Edge cases

- **A home that never upgraded past an earlier wave** updates straight to the removal release:
  Epic 35's update path walks releases in order where a release says it must not be skipped;
  every wave's release is marked so, so a home never lands on a release whose in-process code
  is gone while its stages were never flipped.
- **The light runner fails to start on a home** (a broken sandbox on an old kernel): before the
  removal release, the stages fall back to `inproc` automatically and the Components page says
  why; after it, the home's `core.pipeline` component is *Failed* with the runner's reason, and
  the update path's pre-check (Epic 51.18) refuses to install the removal release on a home
  whose runner check fails.

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

The release flips the eleven at update. The removal (§4) ships one release later.

### Rollback

Before §4: per stage switch and Epic 51 rollback. After §4: Epic 51 release rollback to the
release that still has the in-process code.

### Dependencies

54.35 (every other stage already a plugin for a release).
