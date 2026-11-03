# Story 51.06 — Pipeline stage groups stop without losing work

> Epic 51 · Phase 1 · Depends on: 51.02 · Needed by: 51.07, 51.08, 51.14

## Why

Turning transcription off tonight means, today, pausing `extract` and `transcribe` one by one
on the Queues page. A `stages` component must stop in one move, live, and never lose, fail or
cancel a job (F5, N6). The pipeline already has every piece: the `StageSelector`
(`pipeline/src/maktaba_pipeline/cluster/stages.py`) re-reads its switches and records why a
stage is off in `workers.stages_off`; `job_stages.enabled` with a `disabled_reason` gates
claiming and enqueueing (`engine/claim.py`, `engine/enqueue.py:185`); the shutdown drain
(`pipeline/shutdown.py`) checkpoints and returns jobs without counting an attempt. This story
puts them in the order of README §4.5, adds the catch-up, moves the pipeline's own loops
under a runner, and defines the two CI jobs that prove it.

## Description

### 1. The pipeline component runner

`maktaba_pipeline.components.runner.ComponentRunner`, one per pipeline process (main worker,
each `stt-N`, the index worker), started in `runtime.py` beside the stage-selection
refresher. It uses the 51.02 store: `LISTEN components_changed` through `db/pubsub.py`, a
30 s revision poll (N12), and on a node `component_desired_for(node)`. On every change it:

1. recomputes each component's gate (enabled, not held, requirements met, no
   `env_override`), the same rule as 51.04 and 51.05;
2. calls `StageSelector.compute()` at once (not at the next 30 s refresh), so claiming stops
   within N4 (≤ 2 s);
3. starts the drain of §3 for stages that just left the served set for a component reason;
4. on the home's leader only (the existing advisory lock, `engine/leader.py`), writes the
   `job_stages` rows of §2;
5. starts or stops the pipeline loops of §6;
6. writes its `component_status` rows (`part = 'pipeline'` or `'pipeline/<instance>'`).

`MAKTABA_COMPONENTS_RECONCILE=off` (51.04): the runner computes and reports but changes
nothing, with reason `reconcile_off`.

### 2. Stop claiming (≤ 2 s)

Two layers, both existing mechanisms:

- **This machine.** `select_stages()` gets one more input, `components: Mapping[str, str]`
  (stage → reason). The reason sits after `storage` and before `switched_off`, so the order
  becomes: not_selected, version_ahead, unavailable, home_only, storage,
  **`component_off:<id>` / `component_held:<id>`**, switched_off, draining. It covers the
  home's desired state and this machine's override (51.02 `component_overrides`). It is
  stored in `workers.stages_off` like the others.
- **The whole home.** The leader sets, for every stage of a component that is off at home
  level, `job_stages.enabled = false` with `disabled_reason = 'component_off'` (or
  `'component_held'` for a hold). `claim.py` already requires `s.enabled`, so no machine
  claims, even one whose own runner is slow. `engine/sync.py`'s `stage_enabled_state()` and
  `_UPSERT` learn the two reasons: they are overwritten when the component comes back, like
  `not_configured` and `extension_disabled`; **`operator` is never overwritten** (51.03).
  A stage paused on the Queues page (`job_stages.paused`) stays paused: both must clear for
  the stage to run (README §6.3).

### 3. Drain and return (≤ `drain.grace_s`)

For in-flight jobs of the stages that left the served set on this worker:

1. Set `pause_requested = true` on exactly those jobs (`claimed_by = <worker>` and
   `stage = ANY(<stages>)`), the same cooperative flag the shutdown drain uses. Stages that
   checkpoint (transcription saves finished segments, `audio/extract.py` checks the flag)
   checkpoint and move to `paused` on their own.
2. Wait up to the catalog's `drain.grace_s`: 30 s by default, 120 s for `transcription`
   (N5). Other stages keep running untouched.
3. At the deadline, force-pause the stragglers with the shutdown drain's statement
   (`_FORCE_PAUSE_*` in `pipeline/shutdown.py`), restricted to those stages, with
   `paused_reason = 'component_off'`, then cancel their tasks.

A `paused` job is claimable again later and `claim.py:172` does not add an attempt for it,
so the job is **returned**: not failed, no attempt counted, no failure class. Its
checkpoint (`paused_at_sec`) is kept. The cancelled task's `CancelledError` path
(`engine/run.py`, `engine/dispatch.py`) already ends as "interrupted, paused" and writes
nothing else. The drain is a new function `pipeline/shutdown.py:drain_stages()`, sharing the
SQL with `ShutdownOrchestrator`.

### 4. Don't feed, keep queued

- `enqueue_job()` (`engine/enqueue.py`): when the stage is disabled with
  `disabled_reason = 'component_off'`, return `EnqueueResult(None, "skipped_component_off")`
  instead of `"disabled"`. `propagate.py` logs the edge with `kind = "skipped"`, message
  `component off`. No row is inserted.
- When `disabled_reason = 'component_held'`, enqueue **as usual**: a hold is a temporary stop (it lasts until Start),
  so new work waits in the queue and runs when the component is started (H2, H4).
- Jobs already queued (`pending`, `paused`) **stay**. Nothing cancels, deletes or parks them:
  the disabled stage is the gate. No `parked_on` is written, so Epic 40's `park_max_hours`
  backstop never touches them.
- The Queues page reason: the API's waiting reason gets `component_off` / `component_held`
  (`api/internal/handlers/queues/types.go`, `snapshot.go`) with the component id; the web
  shows `queue.waiting.component_off` ("Waiting: {component} is off") and
  `queue.waiting.component_held` ("Waiting: {component} is stopped"), and the Machines page
  `machines.reason.component_off` / `component_held` (parsed by
  `web/src/components/cluster/format.ts` like `paused: <why>`). English and Arabic in the same
  PR. Epic 40 health treats these as expected, not red (51.14 adds the tile).

### 5. Enable: release and catch up

When a component comes back at home level, the leader:

1. sets its stages' `job_stages.enabled = true` where the reason was `component_off` or
   `component_held` (queued jobs become claimable at once);
2. for `component_off` only, enqueues one **`component_catchup`** job (new core `StageDef`:
   subject `catalog`, pool `cpu`, placement `home`; its `job_stages` row is seeded by 51.02),
   payload `{"component": "<id>", "off_since": "<ts>"}`. `off_since` is the time the leader
   disabled the stages, kept in its status row's `detail`; unknown (off since the seed) means
   all history.

The catch-up handler walks, for each stage S of the component and each upstream edge U → S
in the graph, the `done` jobs of U with a success outcome finished since `off_since`, in
batches of 500 ordered by id, and re-runs the existing propagation for those jobs restricted
to S (`propagate.py`, new `enqueue_dependents(db, job, only=S)`), at bulk priority 500. The
existing debounce and "already queued" rules prevent doubles. Progress is the job's progress
(`jobs_progress.py`: done / total), shown on the Queues page like any job. Entry stages
(`youtube_sync`) need no catch-up: the reconciler's own schedule picks them up.

### 6. Pipeline loops under the runner

Loops started in `runtime.py` become declared loops of the runner (asyncio tasks with their
own stop event; stop grace from the catalog, default 5 s):

| Loop (catalog name) | Component |
|---|---|
| `channel-topup` | `livetv` |
| `queue-health` | `queue_health` |
| `health`, `cluster-agent` | `cluster` |
| `worker-hb`, `stage-selection`, `reconciler`, `pruner`, `audio-cache-sweep`, `claim-lease`, `index-close` | `core.pipeline` (declared, never stopped) |

The declared table is compared with `catalog.loops_for("pipeline")` in the `catalog` job
(51.01), the pipeline half of F1. `MAKTABA_CLUSTER_AGENT=0` keeps working as an
`env_override`. The in-process semantic search (`grpc_server.py`, `Embed` / `Search`)
follows the `search.semantic` gate and answers `UNAVAILABLE` while it is off.

### 7. The CI jobs

**`components-live`** (`.github/workflows/_components.yml`, job `components-live`, gated by
a new `components` filter in `.github/ci-paths.yml`: the runners, the reconciler, the
catalog, the stores, `engine/claim.py`, `engine/enqueue.py`, `engine/sync.py`,
`pipeline/shutdown.py`). On `ubuntu-22.04`, a scratch Postgres 16 with pgvector, the real
`maktaba-server serve` (supervisor, API, streaming, pipeline) built from the commit, a
second pipeline worker started as a joined machine, and a test-only STT backend `sleep`
(installed from `tests/components/sleepy/` into the CI venv only: each segment sleeps 1 s,
writes fixed words and checkpoints). Media: `shared/fixtures/samples`. It measures and
asserts:

| # | Measure | Target |
|---|---|---|
| N1 | desired change seen by both pipeline runners | p95 ≤ 2 s over 50 changes; ≤ 30 s with `NOTIFY` blocked |
| N2 | `loop` / `stages` component click → Running | p95 ≤ 5 s |
| N3 | `process` component click → Running | p95 ≤ 15 s, plus warm-up reported apart |
| N4 | stop claiming after disable or hold | ≤ 2 s (no claim of the stages after it, from either machine) |
| N5 | drain | ≤ `drain.grace_s` (30 s; 120 s transcription) |

plus: queued counts unchanged by a disable; zero `failed` or `cancelled` jobs caused by it;
catch-up enqueues exactly the skipped edges. Server logs and the pipeline's logs are kept as
an artifact on every run (standing rule: tests keep logs).

**`components-chaos`** (CI only; same workflow, run nightly by a scheduled caller and on
PRs touching the runners). 100 random enable / disable / hold / start flips of
`transcription`, `media.thumbnails`, `classify` and `search.semantic`, one every 3–20 s,
while 2,000 jobs run. At the end, everything enabled and the queue drained: every job is
`done`; **zero** jobs ever went to `failed` or `cancelled` because of a flip; no job's
`attempts` exceeds its count without flips (N6). A failure keeps the full job history as an
artifact.

## Acceptance criteria

- **Given** two `transcribe` jobs running and 500 queued,
  **when** an admin disables `transcription`,
  **then** no machine claims a `transcribe` or `extract` job after 2 s; the two running jobs
  checkpoint and move to `paused` within 120 s with their attempt count unchanged; the 500
  stay queued; no job is `failed` or `cancelled`.
- **Given** `transcription` off,
  **when** a `content_hash` job finishes whose edge leads to `transcribe`,
  **then** no `transcribe` job is created and the edge is logged as `skipped_component_off`.
- **Given** `transcription` held (not disabled),
  **when** the same edge fires,
  **then** a `transcribe` job is queued and waits.
- **Given** `transcription` was off for a day with 300 skipped edges,
  **when** it is enabled,
  **then** queued jobs are claimable within 5 s and one `component_catchup` job queues
  exactly those 300, with progress shown.
- **Given** an operator disabled `youtube_fetch` on the Queues page,
  **when** `youtube` is disabled and enabled again,
  **then** `youtube_fetch` is still disabled with `disabled_reason = 'operator'`.
- **Given** node N has an override `transcription = installed`,
  **when** the home keeps it enabled,
  **then** N's `workers.stages_off` says `component_off:transcription` and the home's
  workers keep claiming.
- **Given** `components-chaos` nightly,
  **when** it runs 100 flips under load,
  **then** zero jobs are lost, failed or cancelled (N6).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | `select_stages` with `components={"transcribe":"component_off:transcription"}` | select | reason in `off`; order after `storage`, before `switched_off` |
| T02 | unit | `stage_enabled_state` with each reason and an `operator` row | sync | `component_*` overwritten on enable; `operator` kept |
| T03 | unit | `enqueue_job` on a `component_off` stage | enqueue | `skipped_component_off`, no row |
| T04 | unit | same on `component_held` | enqueue | row inserted |
| T05 | integration (PG) | one running checkpointing job | `drain_stages` | `paused`, `paused_reason = component_off`, attempts unchanged, checkpoint kept |
| T06 | integration (PG) | a job ignoring the flag | `drain_stages` with grace 2 s (test) | force-paused at the deadline; task cancelled; no failure class |
| T07 | integration (PG) | jobs of other stages in flight | drain | untouched |
| T08 | integration (PG) | 300 upstream successes since `off_since` | run `component_catchup` | 300 jobs, batches of 500, progress 300/300 |
| T09 | integration (PG) | catch-up twice | run | no duplicates |
| T10 | unit | runner, counting fake store | idle 5 min | ≤ 10 queries (N12) |
| T11 | unit | declared loops vs catalog | compare | equal |
| T12 | unit | `reconcile = off` | disable | nothing changes; `reconcile_off` reported |
| T13 | unit (web) | `format.ts` with `component_off:transcription`, locale ar | format | Arabic reason with the Arabic component title |
| T14 | unit (api) | snapshot with a `component_off` stage | build | waiting reason `component_off` + component |
| T15 | CI | `components-live` | the table of §7 | N1–N5 met |
| T16 | CI | `components-chaos` | 100 flips under load | N6: zero lost/failed/cancelled |

## Edge cases

- **The home pipeline is down.** Nodes still stop claiming from their own runner;
  `job_stages` is flipped when the leader comes back.
- **A component off while a catch-up runs.** The catch-up job's own stage is core; its
  `enqueue` calls get `skipped_component_off` and it ends; the next enable starts a new one.
- **Stages shared by intent** (none today): the catalog forbids a stage in two components
  (51.01), so one component's gate never fights another's.
- **A worker restarted mid-drain** goes through the normal shutdown drain; same end state.
- **SQLite rigs** have no `NOTIFY`: changes apply within the 30 s poll; N1/N4 are Postgres
  targets.
- **Disable then enable within the grace.** The drain stops; jobs already flagged finish
  their checkpoint and are claimed again; nothing is lost.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/components/runner.py` (new) | §1, §6. |
| `pipeline/src/maktaba_pipeline/components/stages.py` (new) | Stage → reason map from desired state and overrides. |
| `pipeline/src/maktaba_pipeline/cluster/stages.py` | `components` input and reasons (§2). |
| `pipeline/src/maktaba_pipeline/engine/sync.py` | `component_off` / `component_held` in `stage_enabled_state` and `_UPSERT`. |
| `pipeline/src/maktaba_pipeline/engine/enqueue.py` | `skipped_component_off` (§4). |
| `pipeline/src/maktaba_pipeline/engine/propagate.py` | Skip log; `enqueue_dependents(…, only=)`. |
| `pipeline/src/maktaba_pipeline/engine/graph.py` | `component_catchup` `StageDef`. |
| `pipeline/src/maktaba_pipeline/engine/stages/component_catchup.py` (new) | The catch-up handler (§5). |
| `pipeline/src/maktaba_pipeline/pipeline/shutdown.py` | `drain_stages()` sharing the force-pause SQL. |
| `pipeline/src/maktaba_pipeline/runtime.py` | Start the runner; loops of §6 declared. |
| `pipeline/src/maktaba_pipeline/grpc_server.py` | Semantic methods follow the gate. |
| `api/internal/handlers/queues/types.go`, `snapshot.go` | Waiting reasons `component_off`, `component_held`. |
| `web/src/components/cluster/format.ts` | Parse `component_off:<id>`. |
| `web/src/i18n/en.json`, `web/src/i18n/ar.json` | `queue.waiting.component_off`, `queue.waiting.component_held`, `machines.reason.component_off`, `machines.reason.component_held`, English and Arabic. |
| `pipeline/tests/components/test_runner.py`, `test_stage_groups.py`, `test_catchup.py` (new) | T01–T12. |
| `api/internal/handlers/queues/snapshot_component_test.go` (new) | T14. |
| `web/src/components/cluster/format.test.ts` (new) | T13. |
| `tests/components/test_live.py`, `tests/components/test_chaos.py`, `tests/components/sleepy/` (new) | The two CI jobs' drivers and the test STT backend. |
| `.github/workflows/_components.yml` (new) | Jobs `components-live` and `components-chaos`, logs kept as artifacts. |
| `.github/workflows/components-nightly.yml` (new) | Nightly schedule calling `components-chaos`. |
| `.github/workflows/ci.yml`, `.github/ci-paths.yml` | Call `_components.yml`; the `components` filter. |

### Tests

- Unit: T01–T04, T10–T14 (pipeline `uv run --extra dev pytest`, mypy strict covers the
  tests; Go and vitest for T13/T14).
- Integration: T05–T09 on the pipeline's Postgres tests in the `integration` job.
- **CI only:** **`components-live`** (N1–N5) on PRs touching the `components` filter;
  **`components-chaos`** (N6) nightly and on runner changes. Both are README §11 jobs and
  are defined here; 51.04 and 51.05 add their scenarios to `components-live`.

### Rollout

Phase 1, dark: seeded desired state enables every stage group that runs today, so the runner
flips nothing. `job_stages` rows are unchanged (proven by update-rehearsal scenario 12,
51.03).

### Rollback

`[components] reconcile = off`: the runner acts on nothing; `job_stages` rows with a
`component_*` reason are left as they are until the next enable, so the runbook says to
enable the component (or clear the reason by hand) before turning the reconciler off.

### Dependencies

51.02 (store, the `component_catchup` row). 51.07 builds on the runner for extensions.
