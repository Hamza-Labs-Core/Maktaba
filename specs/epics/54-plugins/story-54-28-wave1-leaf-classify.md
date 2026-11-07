# Story 54.28 — Wave 1: classify's leaf stages

> Epic 54 · Phase 4 · Depends on: 54.26, 54.27 · Needed by: 54.29

## Why

The first wave proves the whole path (manifest, runner, statements, switch, parity, release
flip, rollback) on the stages that can hurt least: they read the database, write the database,
touch no file, call no model and no network, and few stages depend on them (README §15.9).

## Description

### 1. The stages

| Stage | Today | Pool | Placement | Downstream |
|---|---|---|---|---|
| `promo_links` | `engine/stages/promo_links.py` + `classify/` | `cpu` | any | review pages |
| `clip_groups` | `engine/stages/clip_groups.py` + `classify/` | `cpu` | any | review pages |
| `topic_collections` | `engine/stages/topic_collections.py` + `classify/` | `cpu` | any | review pages |
| `rate_style` | `engine/stages/rate_style.py` + `classify/production_style.py` | `cpu` | any | review pages |
| `display_title` | `engine/stages/display_title.py` + `classify/display_title.py`, `channels/` | `cpu` | home | apps (titles) |

They become stages of **`tv.maktaba.classify`** (the rest of classify follows in wave 4), runner
group `light`, no `net`, no `files`, no `exec`.

### 2. The work

1. `git mv` the five stage modules and the `classify/` parts only they use into
   `plugins/first-party/classify/src/maktaba_fp_classify/`; shared parts used by later-wave
   stages go to `_lib` (54.27 §1).
2. Replace every `db.fetch*` / `db.execute` in the moved code with `api.stmt` / `api.batch` on
   named statements in `queries/` (about 45 statements for these five).
3. Move each stage's input and output fingerprints into the plugin's `fingerprints` module.
4. Write the `[[stage]]` entries; remove the five `StageDef`s from `GRAPH`; `graph-golden`
   stays green.
5. `[trusted] db.read`, `db.write` and `settings` from what the statements touch (the build
   check of 54.24 refuses a mismatch); a `NNNN_fp_roles` migration for `maktaba_fp_classify`.
6. `parity.toml` with the tables, ignored columns, and failure cases (a library with no
   series, an empty topic set).
7. A migration that sets `impl = 'plugin'` for the five at update (54.25 §3).

The in-process modules stay importable for one release (behind `impl = inproc`), then the
release after next removes them unless a stage was flipped back.

## Acceptance criteria

- **Given** the parity fixture,
  **when** `plugins-parity --stages promo_links,clip_groups,topic_collections,rate_style,display_title`
  runs,
  **then** rows, results, fingerprints and enqueued jobs are identical, and N16 holds for each.
- **Given** a home updated mid-queue to this release,
  **when** the update finishes,
  **then** the five stages run in the `light` runner, no queued job was lost or rerun, and the
  Queues page shows them as built-in plugin stages.
- **Given** the owner flips `display_title` back to `inproc`,
  **when** jobs run,
  **then** they run in-process, write the same data, and the flip is audited.
- **Given** a rollback to the previous release,
  **when** it runs,
  **then** all five run in-process again with their queues intact.
- **Given** the `light` runner,
  **when** a `display_title` job runs,
  **then** it makes no network connection and opens no library file.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | parity (`plugins-parity`) | fixture | the five stages | identical; N16 |
| T02 | unit (`fp-classify`) | moved pure functions | existing unit tests, moved | green, unchanged assertions |
| T03 | contract (`fp-classify`) | SDK harness, statements against test PG under `maktaba_fp_classify` | each handler | same as parity on a small case |
| T04 | CI (`plugins-trusted`) | the plugin's role | forged `subject_id`, unregistered statement, write to `videos.state` | 400 / 403 / permission error |
| T05 | update rehearsal scenario 16.1 | home mid-queue | update, then rollback | no job lost or rerun; impl per release |
| T06 | CI (`graph-golden`) | assembled graph | compare | equal |
| T07 | CI (`components-chaos`) | flips of `promo_links` under load | 100 flips | N21 |

## Edge cases

- **A cluster mid-rollout** (some machines on the previous release): they claim the four
  `placement:any` stages in-process; output is identical, so either side may run any job.
- **`display_title` reads the channel module** (`channels/`), which live TV (wave 7) still uses
  in-process: the shared part moves to `_lib`.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `plugins/first-party/classify/` (new: manifest, queries, src, tests, parity.toml, i18n) | §2. |
| `plugins/first-party/_lib/` | Shared classify helpers. |
| `pipeline/src/maktaba_pipeline/engine/graph.py`, `stages/registry.py` | Remove the five `StageDef`s; keep `CORE_MODULES` entries one release. |
| `shared/db/migrations/NNNN_fp_classify_role.sql`, `NNNN_wave1_impl.sql` (+ `.sqlite.sql`) | Role; `impl = plugin`. |
| `tests/update_rehearsal/scenario_16_1_wave1.*` | T05. |

### Tests

- Parity T01, unit T02, contract T03, CI T04, T06, T07, rehearsal T05.

### Rollout

The release flips the five at update. The release after next deletes their in-process code.

### Rollback

Per stage: `maktaba-server stages impl <stage> inproc`. Per release: Epic 51 rollback.

### Dependencies

54.26 (parity), 54.27 (layout and packaging).

## Built (2026-11-06)

Wave 1 moved `promo_links`, `clip_groups`, `topic_collections` and `rate_style` into
`tv.maktaba.classify` (runner group `light`). `display_title` waits for wave 4: it asks the
language model when the model is on (README §15.12). The flip at update is each worker's graph
sync (README §15.12); the role is migration 0200.
