# Story 54.29 — Wave 2: thumbnails and subtitles

> Epic 54 · Phase 4 · Depends on: 54.28 · Needed by: 54.30

## Why

The second wave adds what wave 1 left out: reading media files, running ffmpeg, writing files
(thumbnails under the home's data folder, subtitle files next to videos), and the first legacy
stage, `thumbnail`, whose handler chains by hand.

## Description

### 1. The stages

| Stage | Plugin | Today | Pool | Placement | Trusted needs |
|---|---|---|---|---|---|
| `thumbnail` (legacy) | `tv.maktaba.thumbnails` | `thumbnail/` module, legacy output fingerprint in `stages/legacy.py` | `nas_read` | home | `files.read:library`, `files.write:data.thumbnails`, `exec:ffmpeg` |
| `thumb_small` | `tv.maktaba.thumbnails` | `stages/thumb_small.py` | `nas_read` | home | same |
| `chapter_thumbs` | `tv.maktaba.thumbnails` | `stages/chapter_thumbs.py` | `nas_read` | home | same |
| `subtitle_gen` | `tv.maktaba.subtitles` | `stages/subtitle_gen.py`, `handlers.py:subtitle_handler` | `nas_write` | any | `files.write:library.subtitles` |

Runner group `light` for both plugins.

### 2. Legacy first

Before the move, `thumbnail`'s hand chaining becomes declared edges with `legacy = False` and
an input fingerprint (a separate PR in this story, landed and green in parity against the old
behaviour first): the jobs it enqueued by hand are enqueued by the engine from the edge, and
the parity fixture checks the same jobs appear.

### 3. Files

- `files.read:library`: the runner gets the library roots of this machine (the share-path
  mapping, `sharepath/`) in its sandbox; a handler asks `api.media_path(video)` for a path,
  which the API answers only for the job's subject.
- `files.write:data.thumbnails`: `<data>/thumbnails/` and its sprite folders.
- `files.write:library.subtitles`: the sandbox allows writes next to media **only** for names
  matching the subtitle pattern Maktaba writes (`<video stem>.<lang>.vtt|srt`); on Linux,
  Landlock can't filter names, so the runner's write helper enforces it and the API refuses a
  `media_path` for anything but the job's video.

### 4. The work

As wave 1 (move code, named statements, fingerprints, manifests, roles, `parity.toml`, impl
migration), plus the legacy conversion of §2.

## Acceptance criteria

- **Given** the parity fixture,
  **when** `plugins-parity` runs the four stages,
  **then** rows, the sha256 tree of `data.thumbnails` and of the subtitle files, results,
  fingerprints and enqueued jobs are identical, and N16 holds.
- **Given** `thumbnail` after §2,
  **when** a probe finishes,
  **then** the engine enqueues the same jobs the hand chaining did (parity of `job_events`).
- **Given** the `light` runner,
  **when** `subtitle_gen` tries to write `evil.sh` next to a video,
  **then** the write is refused and the job fails `plugin_scope`.
- **Given** an update mid-queue and a rollback,
  **when** they finish,
  **then** no job is lost or rerun, and posters and subtitles stay byte-identical.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | parity | fixture with sample media | four stages | identical; N16 |
| T02 | parity | legacy `thumbnail` before and after §2, both in-process | probe → thumbnail | same jobs enqueued |
| T03 | unit (`fp-thumbnails`, `fp-subtitles`) | moved tests | run | green |
| T04 | integration (CI only, three OSes) | runner sandbox | write outside the subtitle pattern, read outside library roots | refused |
| T05 | update rehearsal scenario 16.2 | mid-queue | update and rollback | as AC |
| T06 | CI (`graph-golden`) | assembled graph | compare | equal (the legacy flag change is in the golden diff of §2's PR) |

## Edge cases

- **A NAS that drops mid-job**: the job fails with today's cause (`nas_unavailable`), since the
  handler code is unchanged; parity's failure cases cover it.
- **A video renamed between claim and write**: `media_path` is resolved per call, as the
  in-process code resolves it today.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `plugins/first-party/thumbnails/`, `plugins/first-party/subtitles/` (new) | §1–§4. |
| `pipeline/src/maktaba_pipeline/engine/graph.py`, `stages/legacy.py`, `handlers.py` | §2; remove moved definitions. |
| `pipeline/src/maktaba_plugin_runner/sandbox/` | Subtitle name rule. |
| `shared/db/migrations/` | Roles; `impl = plugin` for the four. |
| `tests/update_rehearsal/scenario_16_2_wave2.*` | T05. |

### Tests

- Parity T01, T02; unit T03; integration T04 (CI only); rehearsal T05; CI T06.

### Rollout

The release flips the four at update. The release after next deletes their in-process code.

### Rollback

Per stage switch; per release Epic 51 rollback.

### Dependencies

54.28 (wave 1 green in a tagged release).

## Built (2026-11-07)

Wave 2 moved `thumb_small` and `chapter_thumbs` into `tv.maktaba.thumbnails` and
`subtitle_gen` into `tv.maktaba.subtitles` (runner group `light`; roles in migration 0201).
The small copy's paths moved to `maktaba_pipeline.thumbnail.small`, which the convert, the
review pages and the mediaintel import read too; the chapter frames use the same cache root
(`MAKTABA_THUMBNAIL_ROOT`), which the API already read them from. The legacy `thumbnail`
stage, with the §2 conversion, follows in its own pull request (README §15.12). Paths come
from the job's own rows rather than a `media_path` call, `library.subtitles` is a Seatbelt
name rule plus the runner's own guard, and a sandbox refusal runs that job in-process
(README §15.12). Parity compares the thumbnail cache and the library's files byte for byte.
