# Story 54.06 — Plugin stages in the queue: events, schedules, dispatch

> Epic 54 · Phase 1 · Depends on: 54.02, 54.04 · Needed by: 54.07, 54.20

## Why

The owner asked for a "queue extender". Maktaba's queue already knows how to claim, retry,
drain, park and explain jobs (Epics 6, 40, 51). A plugin stage should be a normal stage in that
queue, shown on the Queues page with its own tile, with nothing new to learn for the owner,
while its code runs in the plugin host instead of the worker.

## Description

### 1. The plugin loader

`pipeline/src/maktaba_pipeline/plugins/loader.py` turns each **enabled** plugin install (54.07)
into a `PluginExtension` that satisfies the existing `Extension` protocol
(`extensions/base.py`), so the graph build path is the one built-in extensions use:

- `manifest`: name `plugin:<id>`, version, stage names (`p_<hash>_<name>`, 54.01 §4).
- `register(graph)`: for each `[[stage]]` a `StageDef` with `extension = "plugin:<id>"`,
  `pool = "plugins"`, `lane = "media"` (or `"sources"` for catalog stages), `placement = "home"`,
  `max_attempts = 2`, `version` from the manifest, `may_request` mapped to hashed names,
  `external_write = False`; for each trigger an `Edge` from the event's core stage
  (`shared/plugins/events.toml`) with `when = "output_changed"`; for each schedule
  `every_minutes_rule = ("plugin:<id>", <setting>)`.
- `impls()`: one handler for every plugin stage, `plugins.dispatch.run_plugin_stage`.
- `is_configured()`: true when its settings validate (54.11).

Discovery adds a third source to `extensions.discover()`: built-ins, entry points, and plugin
installs read from the database. The plugin loader **never imports plugin code**; it reads
manifests only.

### 2. The `plugins` pool

New `PoolDef("plugins", "Plugins", "global", None, default_size)` with default
`max(1, cores // 4)` on the home. Global (claim SQL) so the API and the Queues page see one
number. Each plugin's `resources.concurrency` caps its share inside it, enforced by the claim
(a per-extension cap in the claim SQL, the way `network_*` pools cap one external system).

### 3. Dispatch

`plugins/dispatch.py:run_plugin_stage(db, job, ctx)`:

1. `POST /internal/plugins/invocations {job_id}` (54.04). A refusal (`plugin_disabled`,
   `plugin_revoked`, `grants_needed`) returns a `StageResult` with outcome `waiting_inputs` and
   no attempt counted; the component runner (54.07) will already have stopped claiming.
2. Send `Run` to the host over `<data>/run/plugind.sock` with the token, limits from the
   manifest (clamped by the admin's caps), settings and the job's checkpoint.
3. Relay `Progress` to `ctx` (the Queues page shows %).
4. Map `Result`:

| Host outcome | StageResult / failure |
|---|---|
| `ok` | `output_hash`, `outcome = changed/unchanged`, `requests` (already validated by the API), `summary` |
| `trap` | raise with cause `plugin_trap` |
| `timeout` | cause `plugin_timeout` |
| `memory` | cause `plugin_memory` |
| `refused(capability|scope|quota)` | `plugin_capability` / `plugin_scope` / `plugin_quota` (not retried) |
| host unreachable / crashed mid-job | job returned to the queue, no attempt counted (as the shutdown drain does) |

5. `DELETE /internal/plugins/invocations/{id}` in a `finally`.

The new causes are added to the built-in table in `engine/failure_classes.py`, mapped to Epic 40's
classes: `plugin_trap` and `plugin_timeout` → `transient` (one retry), `plugin_memory`,
`plugin_capability`, `plugin_scope`, `plugin_quota` → `item_permanent` (no retry: the same job
would be refused again), and a held or revoked plugin → `dependency:plugin` (the job waits,
spends no attempt). Each cause has English and Arabic words.

### 4. Fingerprints

Input fingerprint for a plugin stage: `sha256(plugin version digest, stage version, grants
generation, settings hash, the event's core stage output hash)`. So a new plugin version or a
setting change reruns only when the stage's `version` or the inputs change, like core stages.
Output fingerprint: the plugin's `output_hash`.

### 5. On the Queues page

Plugin stages get tiles under a **Plugins** lane, labelled with the plugin's name (from its
strings, English or Arabic by the viewer's language) and the stage name, a badge "Plugin" (and
"Unreviewed" when it is, 54.10). Pause, requeue and the failing-items page work as for core
stages; the failing-items page shows the plugin failure causes with a link to the plugin's
logs (54.09).

### 6. Events stay stable

`shared/plugins/events.toml` maps each event to a core stage. When a core stage is renamed or
split, only that file changes; a CI check (`test_plugin_events.py`) fails if an event names a
stage that is not in `GRAPH`, or if an event is removed without bumping the plugin API major.

## Acceptance criteria

- **Given** an enabled plugin with a stage triggered by `transcript.ready`,
  **when** a transcript is written for video V,
  **then** one job for that stage is queued for V, claimed from the `plugins` pool, run in the
  host, and its result recorded like a core stage's.
- **Given** a plugin stage that traps,
  **when** it runs twice,
  **then** the job fails with `plugin_trap` after 2 attempts and appears on the failing-items
  page with the plugin's name.
- **Given** the host is killed mid-job,
  **when** the supervisor restarts it,
  **then** the job is back in the queue with its attempt count unchanged, and runs again.
- **Given** a plugin with `concurrency = 1` and 50 queued jobs,
  **when** workers claim,
  **then** at most one of its jobs runs at a time across the home.
- **Given** a plugin stage requesting a core stage through a forged result,
  **when** the result is applied,
  **then** the request is dropped and `GraphViolation` is logged (the API refused it first).
- **Given** a schedule `every_minutes_setting = "interval"` set to 60,
  **when** the reconciler passes,
  **then** a catalog job is queued at most once per 60 minutes.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (py) | manifest with 2 stages, 1 trigger, 1 schedule | load | StageDefs, edge from mapped core stage, `every_minutes_rule` |
| T02 | unit (py) | `validate()` with plugin stages | build graph | acyclic; names `p_*`; no core edge into plugin stages except mapped events |
| T03 | integration (PG, py) | fake host returning each outcome | dispatch | StageResult / cause per table |
| T04 | integration (PG, py) | host socket closed mid-job | dispatch | job requeued, attempt unchanged |
| T05 | integration (PG, py) | concurrency 1, 50 jobs, 4 workers | claim | ≤ 1 running |
| T06 | integration (PG, py) | settings change / version bump / same | reconcile | rerun / rerun / no rerun |
| T07 | unit (py) | `failure_classes` | new causes | mapped classes; en + ar words |
| T08 | unit (py) | `events.toml` vs `GRAPH` | check | every event's stage exists |
| T09 | e2e (`plugins-e2e`) | sample plugins (Rust, JS, Python, TinyGo) | real host, real API | each runs on a fixture video |
| T10 | unit (web) | Queues page with plugin stages | render | Plugins lane, names in en/ar, badges |

## Edge cases

- **A plugin event fires for thousands of videos at once** (first enable with "run on my
  existing library", or a big scan): jobs queue and drain at the pool's pace; the catch-up is
  Epic 51's batched `component_catchup`.
- **Two plugins on one event**: two independent edges; order is not guaranteed and not needed
  (plugins cannot see each other's outputs unless exported).
- **The `plugins` pool size 0** (an admin set it): plugin stages wait; the tile says "Plugins
  pool is 0".

## Build plan

### Files to touch

| File | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/plugins/loader.py`, `dispatch.py`, `events.py` (new) | §1, §3, §4. |
| `pipeline/src/maktaba_pipeline/extensions/__init__.py` | `discover()` adds plugin installs (database read, no import). |
| `pipeline/src/maktaba_pipeline/engine/graph.py` | `plugins` pool in `POOLS`. |
| `pipeline/src/maktaba_pipeline/engine/claim.py` | Per-extension cap for plugin stages. |
| `pipeline/src/maktaba_pipeline/engine/failure_classes.py` | `plugin_*` causes mapped to Epic 40 classes. |
| `shared/plugins/events.toml` | Final mapping. |
| `web/src/pages/Queue*` (Queues page), `web/src/i18n/{en,ar}.json` | Plugins lane, badges, failure cause words. |
| `tools/ci/tests/test_plugin_events.py` (new) | T08. |
| `pipeline/tests/plugins/` (new) | T01–T07. |
| `tests/plugins/e2e/` (new) | T09. |

### Tests

- Unit T01, T02, T07, T08, T10; integration T03–T06; e2e T09 in `plugins-e2e`.

### Rollout

Dark: no plugin installs, no plugin stages. The `plugins` pool row appears in `worker_pools`
with its default size and is unused.

### Rollback

Release rollback; plugin `job_stages` rows are left and ignored by older releases (they have
no handler; the older graph sync marks unknown stages `not_configured`, as it does for removed
stages today).

### Dependencies

54.02 (host), 54.04 (minting). Epic 51 51.06 for stage-group gating (54.07 builds on both).
