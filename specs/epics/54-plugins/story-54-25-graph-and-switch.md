# Story 54.25 — The graph from manifests and the per-stage switch

> Epic 54 · Phase 4 · Depends on: 54.06, 54.23, 54.24 · Needed by: 54.26, every wave

## Why

Today `GRAPH` in `engine/graph.py` is one tuple naming every stage. When a stage moves into a
plugin, its definition must come from the plugin's manifest, and the worker must be able to
run the stage either in-process or in a runner, chosen per stage and flippable both ways
without losing or rerunning a job (README §15.9, H19).

## Description

### 1. The graph assembled from manifests

`engine/graph.py` keeps the types (`StageDef`, `Edge`, `PoolDef`, `Request`, `StageResult`) and
`POOLS`. A new `engine/assemble.py` builds the graph from:

1. the `StageDef`s still in `GRAPH` (shrinking each wave);
2. the `[[stage]]` entries of the first-party manifests, field for field. The graph reads the
   copies embedded in the server build (`importlib.resources`, the same files the packages are
   built from, 54.27), so a stage is defined even where its package can't run; running it in a
   runner needs the package to be trusted (54.22);
3. the third-party plugin stages and the entry-point extensions, as today (54.06).

A first-party stage keeps its exact name, pool, lane, version and edges. `validate()` runs on
the result as today (acyclic, pools exist, `may_request` known). A stage defined twice (in
`GRAPH` and a manifest) fails the build.

### 2. The golden graph

`tools/plugins/graph_golden.py` exports the assembled graph as canonical JSON (stages sorted,
every field, edges, pools). Before wave 1, the export of today's `GRAPH` plus the built-in
extensions is committed as `shared/plugins/graph.golden.json`. The CI check `graph-golden`
fails when the assembled graph differs from it; a PR that changes the graph on purpose updates
the file in the same PR, and the diff shows exactly what changed.

### 3. The switch

Migration: `ALTER TABLE job_stages ADD COLUMN impl TEXT NOT NULL DEFAULT 'inproc'
CHECK (impl IN ('inproc', 'plugin'))` (expand-only; older releases ignore it).

- **Claim.** The `StageSelector` reads `impl` with the stage row. `inproc` calls the
  registered `StageImpl` as today; `plugin` calls `plugins.dispatch.run_native_stage`, which
  mints a token (`POST /internal/plugins/invocations`), sends `Run` to the stage's runner group
  socket (54.23), and maps `Result` to `StageResult` and failure causes exactly as 54.06 §3
  does for Wasm plugins. A stage whose runner is not serving on this machine is not claimed
  here (as `unavailable` today).
- **Fingerprints.** For `impl = plugin` stages, `propagate` and `enqueue` ask the `fp` runner
  (`Fingerprints`, batched per propagate); for `inproc` stages they call the functions as
  today.
- **Flip** (`PUT /api/queues/{stage}/impl {impl}`, admin only, audited, idempotent; CLI
  `maktaba-server stages impl <stage> <impl>`):
  1. set `job_stages.flipping = true`: no new claims of the stage within 2 s;
  2. wait for in-flight jobs up to the stage's drain grace (Epic 51 §4.5), then force-pause
     the rest with their checkpoints (no attempt spent);
  3. set `impl`, clear `flipping`, `NOTIFY job_stages_changed`;
  4. claims resume.
  Queued jobs keep their rows, attempts, input hashes and history.
- **A migration per wave** flips that wave's stages to `plugin` at update, after the new
  release's runners report `Hello` for them; until then they stay `inproc` (so an update never
  leaves a stage with no implementation).

### 4. The Go API

`GET /api/queues/graph` and `GET /api/queues/{stage}` gain `plugin` (the first-party plugin id
or null) and `impl`. The Queues page shows a small "Built-in plugin" badge and, for admins, the
`impl` with a menu to flip it (strings in English and Arabic). The routes that enqueue a named
stage (`/api/libraries/{id}/convert`, `dedup-scan`, the YouTube routes) are unchanged: the
names are the same.

## Acceptance criteria

- **Given** a first-party manifest that defines `promo_links` with the same fields as `GRAPH`,
  **when** `promo_links` is removed from `GRAPH` and the graph is assembled,
  **then** `graph-golden` passes.
- **Given** a manifest that changes `promo_links`'s pool,
  **when** CI runs,
  **then** `graph-golden` fails naming the field.
- **Given** stage S with 100 queued and 2 running jobs at `impl = inproc`,
  **when** an admin flips it to `plugin`,
  **then** no claim of S happens during the flip, the 2 finish or are force-paused with their
  checkpoints, all 100 stay queued with unchanged attempts, and the next claim runs in the
  runner.
- **Given** the same, flipped back to `inproc`,
  **when** jobs run,
  **then** they run in-process with no job lost or rerun.
- **Given** a viewer or a cloud-linked play sign-in,
  **when** they call `PUT /api/queues/{stage}/impl`,
  **then** 403, and nothing changes.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (py) | assemble with a fixture manifest | build | equal to `GRAPH` for the moved stages; duplicate definition fails |
| T02 | unit (py, CI `graph-golden`) | export | compare | equal to golden; field change detected |
| T03 | integration (PG, py) | 100 queued, 2 running | flip both ways | §3 numbers; 0 lost; attempts unchanged |
| T04 | integration (PG, py) | `plugin` stage with a fake runner returning each outcome | dispatch | `StageResult` and causes as 54.06 §3 |
| T05 | integration (PG, py) | propagate over 1,000 subjects through `fp` | fingerprints | equal to in-process values; N19 |
| T06 | role test (go) | `PUT /api/queues/{stage}/impl` | no one / viewer / cloud-linked play / admin | 401 / 403 / 403 / 200 |
| T07 | CI (`components-chaos`) | a test stage flipped 100 times under load | flips | N21: 0 lost, 0 reruns |
| T08 | unit (web) | Queues page | render | badge and `impl` menu, en + ar |

## Edge cases

- **A flip while the runner is down**: the flip completes; jobs of the stage wait as
  `dependency:plugin` until the runner serves, without spending attempts.
- **A machine on an older release in the cluster** (Epic 35 rolls machines one at a time): it
  ignores `impl` and keeps claiming in-process, which writes the same data (54.26), so a mixed
  cluster is safe during a rollout.
- **Flip requested twice**: idempotent; the second answers the current state.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/engine/assemble.py` (new), `graph.py`, `sync.py` | §1. |
| `pipeline/src/maktaba_pipeline/engine/claim.py`, `propagate.py`, `enqueue.py` | `impl`, fingerprints via `fp`. |
| `pipeline/src/maktaba_pipeline/plugins/dispatch.py` | `run_native_stage`. |
| `tools/plugins/graph_golden.py` (new), `shared/plugins/graph.golden.json` (new) | §2. |
| `shared/db/migrations/NNNN_job_stages_impl.sql` + `.sqlite.sql` | `impl`, `flipping`. |
| `api/internal/handlers/queues/` (`queues.go`, `detail.go`, new `impl.go`) | §4 fields and the flip route, `RequireAdmin`, audit. |
| `cmd/maktaba-server/` CLI | `stages impl`. |
| `web/src/pages/Queue*`, `web/src/i18n/{en,ar}.json` | Badge and menu. |

### Tests

- Unit T01, T02, T08; integration T03–T05; role T06; CI T07.

### Rollout

Ships with every stage `inproc` and the golden file equal to today's graph. Nothing changes
until wave 1's migration.

### Rollback

Release rollback; `impl` is ignored by older releases, which run every stage in-process.

### Dependencies

54.06 (dispatch and causes), 54.23 (runners), 54.24 (statements for the `fp` runner).
