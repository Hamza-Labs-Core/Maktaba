# Story 54.32 — Wave 5: convert, duplicates and the file trash

> Epic 54 · Phase 4 · Depends on: 54.31 · Needed by: 54.33

## Why

These stages change the owner's media: they rewrite files to MP4, merge duplicates, move files
to Maktaba's trash and back, and empty it. A mistake here loses media, so they move after four
waves have proved the path, with the strictest file rules.

## Description

### 1. The stages

| Stage | Plugin | Pool | Placement | Trusted needs |
|---|---|---|---|---|
| `convert_mp4` | `tv.maktaba.convert` | `convert` | home | `files.read:library`, `files.write:library`, `exec:ffmpeg` |
| `dedup_scan` | `tv.maktaba.dedup` | `nas_read` | any | `files.read:library` |
| `dedup_apply` | `tv.maktaba.dedup` | `nas_write` | any | `files.write:library`, `files.write:trash` |
| `trash_purge` | `tv.maktaba.dedup` | `nas_write` | any | `files.write:trash` |
| `file_restore` | `tv.maktaba.dedup` | `nas_write` | any | `files.write:library`, `files.write:trash` |

Runner group `light`. Epic 47's priorities (`EPIC47_PRIORITIES`) move into the manifests as
each stage's default priority; the API routes that enqueue `convert_mp4` (`/api/libraries/{id}/convert`,
"Convert now"), `dedup_scan`, `dedup_apply` and `file_restore` are unchanged (54.25 §4).

### 2. File moves through one helper

Every rename, replace and delete these stages do today goes through `library_mgmt/`'s helpers;
after the move they go through the SDK's `files` helper, which:

- writes the file-action journal row (`file_actions`, Epic 47) through a named statement
  **before** the move and marks it done after, as today;
- refuses any path outside the job's subject (a video, or a duplicate group's videos) resolved
  through `api.media_path`;
- on Linux and Windows, where the sandbox allows writing the whole library root, is the
  enforcement point for "only the job's files"; on macOS the Seatbelt profile is generated per
  library root, and the helper still checks.

### 3. Parity for file changes

The parity fixture's libraries are copied per run; parity compares the sha256 tree of the
library and the trash after each stage, the `file_actions` journal rows, and the database. A
failure case kills the runner between the journal row and the move, and expects today's
recovery (the journal replays) on both paths.

## Acceptance criteria

- **Given** the parity fixture,
  **when** `plugins-parity` runs the five stages,
  **then** the library and trash trees, the journal and the rows are identical, and N16 holds.
- **Given** `dedup_apply` for group G,
  **when** its handler asks to move a file of another group,
  **then** the helper refuses (`plugin_scope`) and nothing moves.
- **Given** the runner killed after the journal row and before the move,
  **when** it restarts,
  **then** the journal replay finishes the move exactly as the in-process path does.
- **Given** an update mid-conversion and a rollback,
  **when** they finish,
  **then** no file is lost or half-written, and no job is lost.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | parity | copied fixture libraries | five stages | identical trees, journal, rows; N16 |
| T02 | integration (py) | helper with a foreign path | move | refused |
| T03 | parity failure case | kill between journal and move | restart | same recovery both paths |
| T04 | update rehearsal scenario 16.5 | mid-conversion | update and rollback | no file lost; job resumes |
| T05 | role test (go) | the enqueue routes | no one / viewer / admin | unchanged answers (401 / 403 / 2xx) |
| T06 | unit (`fp-convert`, `fp-dedup`) | moved tests | run | green |

## Edge cases

- **A read-only share**: the same failure as today (`nas_readonly`).
- **Trash on another volume**: the helper copies then deletes, as `library_mgmt/` does today.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `plugins/first-party/convert/`, `plugins/first-party/dedup/` (new) | §1; `library_mgmt/` conversion and duplicate code moves. |
| `plugins/sdk/python/maktaba_plugin_sdk/native/files.py` (new) | §2 helper. |
| `pipeline/src/maktaba_pipeline/engine/graph.py`, `stages/` | Remove moved definitions after one release. |
| `shared/db/migrations/` | Roles; `impl = plugin` for the five. |
| `tests/update_rehearsal/scenario_16_5_wave5.*` | T04. |

### Tests

- Parity T01, T03; integration T02; rehearsal T04; role T05; unit T06.

### Rollout

The release flips the five at update.

### Rollback

Per stage switch; per release Epic 51 rollback.

### Dependencies

54.31.
