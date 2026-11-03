# Story 54.34 — Wave 7: live TV

> Epic 54 · Phase 4 · Depends on: 54.33 · Needed by: 54.35

## Why

Live TV's stages plan the channels, write the files streaming reads, and stage, swap, clean
up and roll back TV changes across Epic 33's slots. A wrong write is visible at once on every
TV, so they move late, with parity on the channel files and an Epic 33 swap in the rehearsal.

## Description

### 1. The stages

| Stage | Plugin | Pool | Placement | Trusted needs |
|---|---|---|---|---|
| `plan_channels` | `tv.maktaba.livetv` | `cpu` | home | `settings` (`livetv`, `look`) |
| `write_channels` | `tv.maktaba.livetv` | `cpu` | home | `files.write:data.channels`, `files.read:library` |
| `tv_stage` | `tv.maktaba.livetv-slots` | `cpu` | home | `files.write:data.tv-slots` |
| `tv_swap` | `tv.maktaba.livetv-slots` | `cpu` | home | same |
| `tv_gc` | `tv.maktaba.livetv-slots` | `cpu` | home | same |
| `tv_rollback` | `tv.maktaba.livetv-slots` | `cpu` | home | same |

Runner group `light`. `write_channels` is the largest stage by SQL (about 33 call sites,
`CH_WRITE_VERSION = 4`), so it uses `batch` for each channel's write.

### 2. What stays

The loops (channel top-up, the tvchange controller, tvremote, the streaming channel engine)
are Epic 51 `loop` components and do not move (README §15.2). `after_topup` and
`every_minutes_rule` move into the manifests unchanged, so the engine still enqueues these
stages when top-up extends a timeline.

### 3. Parity

The fixture has three channels with a plan; parity compares the channel files and the slot
release folders (sha256 trees), the plan rows, and the `tvchange` rows a swap writes.

## Acceptance criteria

- **Given** the parity fixture,
  **when** `plugins-parity` runs the six stages,
  **then** channel files, slot folders, rows, results and fingerprints are identical, and N16
  holds.
- **Given** an Epic 33 swap in the update rehearsal across the flip,
  **when** the TV change is staged in-process before the update and swapped by the plugin
  after,
  **then** the swap completes, and a rollback brings the previous slot back.
- **Given** a top-up that extends a channel,
  **when** it finishes,
  **then** the engine enqueues the `after_topup` stages as today.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | parity | fixture channels and slots | six stages | identical; N16 |
| T02 | update rehearsal scenario 16.7 | a staged TV change | update mid-swap, rollback | swap completes; rollback restores |
| T03 | integration (PG, py) | top-up | extend timeline | `after_topup` jobs as today |
| T04 | unit (`fp-livetv`, `fp-livetv-slots`) | moved tests | run | green |

## Edge cases

- **A swap in flight when the stage flips**: the flip waits for the stage's drain grace; a TV
  swap is short, and its checkpoint (`tvrelease/`) resumes on either path.
- **`livetv.inprocess` homes** (no slots): the `livetv-slots` plugin is off, as its component is
  today.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `plugins/first-party/livetv/`, `plugins/first-party/livetv-slots/` (new) | §1–§3; `schedule/`, `channels/` stage parts and `tvrelease/` move (shared parts to `_lib`). |
| `pipeline/src/maktaba_pipeline/engine/graph.py`, `stages/` | Remove moved definitions after one release. |
| `shared/db/migrations/` | Roles; `impl = plugin` for the six. |
| `tests/update_rehearsal/scenario_16_7_wave7.*` | T02. |

### Tests

- Parity T01; rehearsal T02; integration T03; unit T04.

### Rollout

The release flips the six at update.

### Rollback

Per stage switch; per release Epic 51 rollback.

### Dependencies

54.33; Epic 33 (slots and swap).
