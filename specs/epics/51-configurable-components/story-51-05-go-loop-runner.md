# Story 51.05 — Go loops start and stop from the catalog

> Epic 51 · Phase 1 · Depends on: 51.02 · Needed by: 51.08, 51.09

## Why

The API starts about 45 named background loops with `safego.Go(logger, "<name>", …)` in
`api/main.go`, `api/cluster_bootstrap.go`, `api/node_bootstrap.go` and
`api/internal/router/p*.go`, most of them with `context.Background()`, so nothing can stop
one without stopping the process. Streaming starts its loops with `bg.Go(…)` (one shared
context, `streaming/lifecycle.go`) in `main.go`, `tvcluster_wire.go` and `slots_wire.go`,
without names. README H14: each Go process gets a **component runner** that starts and stops
its loops by catalog id, each with its own context and stop grace, and reports their status.

## Description

### 1. The package

New module `github.com/Hamza-Labs-Core/Maktaba/shared/go/componentrunner`
(`shared/go/componentrunner/`), used by `api/` and `streaming/`. It imports the catalog and
store modules of 51.01/51.02.

```go
type LoopFunc func(ctx context.Context) error

type Runner struct { /* … */ }

func New(o Options) *Runner           // Host ("api"|"streaming"), Machine, Store, Catalog, Logger, Reconcile bool
func (r *Runner) Declare(name string, fn LoopFunc)   // "tvchange_controller"; panics if host:name is not in the catalog
func (r *Runner) Run(ctx context.Context)            // reconcile loop; returns when ctx ends, after stopping every loop
func (r *Runner) On(component string) bool           // current gate, lock-free read (atomic map snapshot)
func (r *Runner) Declared() []string                 // for the catalog test
```

- **Per-loop context.** Each loop runs in its own goroutine with a child context of the
  runner's. Stopping cancels that context and waits the catalog's `stop_grace_s` (default
  5 s).
- **Stop timeout.** A loop that has not returned after its grace is reported Failed
  `stop_timeout` and logged once with its name. The runner never starts a second copy while
  the first is still running, so a stuck loop can't double up.
- **Crashes.** A loop that returns an error or panics (recovered, as `safego.Go` does) is
  restarted with backoff 1 s doubling to 60 s; 5 crashes in 10 min → Failed `crash_loop`,
  retried after 5 min. The same numbers as the supervisor's restart policy, so the Components
  page reads them the same way.
- **A loop that ends cleanly** (returns `nil` while still wanted) is a one-shot: reported
  Off with `detail.exited = true`, not restarted.
- **Reconcile.** Same level-based pass as 51.04: desired state from the store, the listener
  (`store/pqlisten` in the API, `store/pgxlisten` in streaming), a 30 s revision poll (N12),
  `On(id)` true when the component is enabled, not held, its requirements are not Waiting,
  and no `env_override` forbids it.
- **Kill switch.** `MAKTABA_COMPONENTS_RECONCILE=off` (set by the supervisor from
  `[components] reconcile`, 51.04): every declared loop runs as it runs today, status reports
  `reconcile_off`.
- **Status.** Rows with `part = 'api'` or `'streaming'`, one per component that has a loop in
  this process; phase Running when all its loops here run, Draining while one is stopping,
  Failed with the loop's reason otherwise. `detail.loops` lists each loop's state.

### 2. Loops moved onto the runner

Every loop in research §1.2 and §1.3 that belongs to a switchable component (51.01 §2).
Each `safego.Go` or `bg.Go` start becomes a `Declare` in one table per host, so the catalog
test can see them all whatever the runtime conditions:

| Host | Declared in | Loops (catalog name) | Component |
|---|---|---|---|
| api | `api/internal/router/p33.go` | `tvchange_controller` | `livetv` |
| api | `api/internal/router/p32_remote.go` | `tvremote_controller`, `tvremote_publisher`, `tvremote_health_sampler` | `livetv` |
| api | `api/internal/router/p35.go` | `updates_discoverer`, `updates_scheduler`, `updates_driver` | `updates.auto` |
| api | `api/main.go` | `update_checker` | `updates.auto` |
| api | `api/internal/router/p6.go` | `enrichment_auto_accept` | `enrich` |
| api | `api/main.go` | `cloudlink_reporter` | `cloud.relay` |
| api | `api/main.go` | `watch_reaper`, `usage_counter` | `analytics` |
| api | `api/main.go` | `telemetry_sender` | `telemetry` |
| api | `api/main.go` | `pairing_sweep` | `cluster` |
| api | `api/cluster_bootstrap.go` | `cluster_housekeeping`, `cluster_hub`, `home_link`, `cluster_push_database`, `cluster_ensure_access`, `mdns` | `cluster`, `discovery.mdns` |
| streaming | `streaming/tvcluster_wire.go` | `channel_engine`, `channel_fences`, `channel_sweeper`, `push_relay`, `viewings`, `hdhr_ssdp` | `livetv.inprocess`, `livetv.hdhomerun` |
| streaming | `streaming/slots_wire.go` | `slots_frontdoor`, `slots_ops`, `viewings`, `hdhr_ssdp` | `livetv.slots`, `livetv.hdhomerun` |

Loops of `core.*` (idempotency sweep, process recorder, licence refresher, event bus, JWT key
reaper, queue snapshots, switchover, node-mode loops; streaming's watchdog, cache GC, session
reaper, VOD purge, hwaccel, share map) are also declared, with `core.*` ids, so they report
health; the runner never stops them. Per-request goroutines (`link_classify`,
`clusterauth_node_read`, `cluster_grant_access`, `nodedb_when_up`, `streaming_reconcile`)
stay `safego.Go`: they are not loops.

Every moved loop takes the runner's context instead of `context.Background()` and must return
within its grace when it is cancelled. Loops that close resources on exit (the channel engine's
stations: today an `OnStop` hook) do it before returning.

`livetv.hdhomerun` and the `hdhr_device.enabled` switch (`api/internal/handlers/tv/tv.go`)
are kept in step: the TV settings handler that writes `hdhr_device.enabled` also writes the
component's desired state through the store, with its audit row, in the same transaction.

### 3. `409 component_off`

- **API:** `middleware.RequireComponent(r *componentrunner.Runner, id string)` in
  `api/internal/middleware/component.go`. When `r.On(id)` is false it answers through
  `httperror.Conflict("component_off", …)` with extras `{"code":"component_off",
  "component":"<id>"}` (the API's problem+json envelope, `api/internal/httperror`).
- **Streaming:** the same check in `streaming/internal/httpx` with streaming's error format.
- This story attaches it only to the Live TV routes (`/api/tv/*` in `router/p33.go` for
  `livetv`, HDHomeRun's `discover.json` / `lineup.json` in streaming for `livetv.hdhomerun`)
  to prove it end to end. 51.09 attaches it everywhere else and adds the clients' words.
- The code `component_off` gets words now. The web shows a 409's `detail` when it reads as
  a sentence (`web/src/lib/errorText.ts`), so an English server sentence would reach an
  Arabic screen. `errorText.ts` maps `code: "component_off"` to `error.componentOff`
  ("{component} is turned off on this home"), with `{component}` filled from
  `components.<id>.title`, in `web/src/i18n/en.json` and `ar.json` together. The apps get
  their words in 51.11.

### 4. Catalog check (F1, Go loops)

`componentrunner` exposes `Declared()`. Tests in `api/` and `streaming/` build the declare
tables without starting anything and compare with `catalog.LoopsFor("api")` /
`LoopsFor("streaming")`: equal sets, or the test fails naming the extra or missing loop. They
run in the `catalog` job of 51.01, and the job's filter gains the declare files. A static
check in the same job refuses a new `safego.Go(` or `bg.Go(` in the start files listed in
§2 unless the line carries `// not a loop: <why>`.

## Acceptance criteria

- **Given** `livetv` enabled and the tvchange controller running,
  **when** `livetv` is set to `installed`,
  **then** within 2 s the controller's context is cancelled, it returns within 5 s, status
  goes Draining then Off, and the API process keeps its pid.
- **Given** `livetv` off,
  **when** a client calls `GET /api/tv/changes`,
  **then** it gets `409` with `"code":"component_off","component":"livetv"`.
- **Given** a loop that ignores cancellation,
  **when** its component is disabled,
  **then** after its grace it is reported Failed `stop_timeout` and enabling the component
  again does not start a second copy until the first returns.
- **Given** a loop that panics three times in a minute,
  **when** the runner restarts it,
  **then** the restarts follow 1 s, 2 s, 4 s and the fifth crash in 10 min makes it Failed
  `crash_loop`.
- **Given** `MAKTABA_COMPONENTS_RECONCILE=off`,
  **when** desired state says `analytics = installed`,
  **then** the watch reaper still runs, status says `reconcile_off`.
- **Given** a new `safego.Go(logger, "x", …)` in `api/main.go` not declared in the catalog,
  **when** CI runs,
  **then** the `catalog` job fails.
- **Given** `livetv` enabled again,
  **when** the runner reconciles,
  **then** its loops are Running within 5 s p95 (N2).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | runner, fake store, one loop | disable | ctx cancelled; returned; Off |
| T02 | unit | loop ignoring ctx | disable; enable | `stop_timeout`; no second copy until it returns |
| T03 | unit | panicking loop, fake clock | run | backoff 1/2/4 s; `crash_loop` after 5 in 10 min; retried after 5 min |
| T04 | unit | loop returning nil | run | Off, `exited`; not restarted |
| T05 | unit | `Declare("nope")` | build | panic names `api:nope` |
| T06 | unit | reconcile off | disable component | loop keeps running; `reconcile_off` |
| T07 | unit | counting fake store | idle 5 min | ≤ 10 queries (N12) |
| T08 | unit (api) | declare table vs catalog | compare | equal sets |
| T09 | unit (streaming) | same | compare | equal sets |
| T10 | unit (api) | `RequireComponent` with gate off | request | 409 problem+json with `code`, `component` |
| T11 | unit (api) | each moved loop | start, cancel | returns within its grace |
| T12 | role test (api) | `/api/tv/changes` with `livetv` off | as no one / viewer / cloud-linked play sign-in / admin | 401 / 403 / 403 / 409 (the role check runs before the component check, so nobody learns more than their role allows) |
| T13 | unit (api) | TV handler writes `hdhr_device.enabled = false` | commit | component `installed`, one audit row, same transaction |
| T14 | unit (web) | `errorText` with a 409 `component_off` for `livetv`, locale ar | render | the Arabic `error.componentOff` with the Arabic Live TV title; never the server's English detail |
| T15 | CI | `components-live` (51.06) | loop scenarios | N2, N4 |

## Edge cases

- **Nodes.** Node-mode loops are `core.node`; a node's API runner reads
  `component_desired_for(node)` and reports through `component_status_report`.
- **Database down at start.** The runner starts loops from the bundle defaults (`full`
  in Phase 1, the same as today) and reconciles once the store answers; it never blocks the
  API's start.
- **Loops sharing state** (`tvremote_*`): they belong to one component, so they stop and
  start together.
- **`viewings` and `hdhr_ssdp`** are declared in both streaming wire files; only one wire
  runs per process (slots or in-process), so each name is declared once at runtime.
- **Supervised API mDNS** is skipped today when supervised; it stays skipped (the
  supervisor's loop, 51.04, is the one advertiser).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/go/componentrunner/go.mod` (new) | Module, `replace` for the catalog and store modules. |
| `shared/go/componentrunner/runner.go` (new) | §1. |
| `shared/go/componentrunner/runner_test.go` (new) | T01–T07. |
| `api/go.mod`, `streaming/go.mod` | Require + `replace` the module. |
| `api/internal/components/loops.go` (new) | The API's declare table and runner start. |
| `api/main.go`, `api/cluster_bootstrap.go` | Loops of §2 moved to the table; runner started after the database pool. |
| `api/internal/router/p6.go`, `p32_remote.go`, `p33.go`, `p35.go` | Loops moved; `/api/tv/*` wrapped with `RequireComponent`. |
| `api/internal/middleware/component.go` (new) | `RequireComponent`. |
| `api/internal/handlers/tv/tv.go` | `hdhr_device.enabled` writes through to the component. |
| `streaming/components.go` (new) | Streaming's declare table and runner start. |
| `streaming/main.go`, `streaming/tvcluster_wire.go`, `streaming/slots_wire.go` | `bg.Go` loops of §2 become declared loops. |
| `streaming/internal/httpx/component.go` (new) | The streaming 409 check. |
| `api/internal/components/loops_test.go`, `streaming/components_test.go` (new) | T08, T09, T11. |
| `api/internal/middleware/component_test.go` (new) | T10, T12. |
| `api/internal/handlers/tv/tv_component_test.go` (new) | T13. |
| `tools/ci/catalog_lint_loops.py` (new) | The "no bare start" check of §4. |
| `.github/ci-paths.yml` | `catalog` filter gains the declare files. |
| `web/src/lib/errorText.ts`, `web/src/lib/errorText.test.ts` | Map `component_off`; T14. |
| `web/src/i18n/en.json`, `web/src/i18n/ar.json` | `error.componentOff`, English and Arabic. |

### Tests

- Unit: T01–T11, T13 (`go test` in `shared/go/componentrunner`, `api`, `streaming`); T14
  in the web unit job (vitest).
- Role tests: T12 (no one, viewer, cloud-linked play sign-in, admin).
- CI: the `catalog` job (T08, T09, static check); **`components-live`** (defined in 51.06)
  runs the loop scenarios for N2 and N4.

### Rollout

Phase 1, dark: seeded desired state enables every loop that runs today, so the runner starts
the same set. Status rows appear; nothing visible changes.

### Rollback

`[components] reconcile = off` (via `MAKTABA_COMPONENTS_RECONCILE=off`) runs every declared
loop unconditionally, as today.

### Dependencies

51.02 (store, listener). The supervisor half of the kill switch is 51.04; built in parallel.
