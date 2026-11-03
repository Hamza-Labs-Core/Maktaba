# Story 51.04 — The supervisor reconciles process components

> Epic 51 · Phase 1 · Depends on: 51.02 · Needed by: 51.08, 51.16

## Why

The supervisor already starts and stops processes at runtime: `Supervisor.Want(role,
instance, on)` (`cmd/maktaba-server/internal/supervisor/run.go:1024`), used today only by the
TV front door (slots) and the relay. Every other optional process (ollama, the `stt-N`
transcription workers, the index worker) is decided once, at boot, from `server.toml` in
`cmd_serve.go`, and changing it needs `/config/restart`. This story makes the supervisor read
desired state and start or stop `process` components live, in order, and report what it did.

## Description

### 1. Build every process the machine could run, start only the wanted ones

At boot the supervisor builds the process table as today, plus every catalog process this
machine **could** run but that today's config left out:

| Component | Processes | Built when |
|---|---|---|
| `llm` | `ollama/` | the payload or `[llm] binary` has an ollama and `[llm] url` is empty |
| `transcription` | `pipeline/stt-1..N` | the pipeline role runs; N from today's plan (`stt.go`), at least 1 when the plan says 0 and the machine can transcribe |
| `search.semantic` | `pipeline/index` | the pipeline role runs and `[pipeline] index_worker` is on |
| `cloud.link` | `relay/` | the agent binary exists and the machine is the home (never licence-gated: it does the claim, 51.19) |
| `livetv.slots` | `tv-slot/blue`, `green`, `amber` | the streaming role runs |

Each process gets a new gate, `allowed`. A process runs only when `allowed && wanted`:

- `allowed` is the reconciler's: the component is `enabled`, not held, its requirements are
  Running, and nothing in §5 blocks it.
- `wanted` stays with today's owners: the TV front door for slots (`/slots/{i}/start|stop`),
  the frozen-home rule for the relay. Processes with no other owner (ollama, stt, index) are
  `wanted` whenever allowed.

New method `Supervisor.Allow(role, instance string, on bool) error`, next to `Want`. On
`false` it stops the process the way `Want(false)` does, with one change: it waits the
component's drain grace, not the fixed `wantStopGrace` (10 s). The grace is
`drain.grace_s + 10 s` from the catalog (transcription: 130 s, so the worker's own 120 s
shutdown drain in `pipeline/shutdown.py` finishes before the kill), or the process's
`StopGrace` when longer. A stop asked by `Allow(false)` is never followed by a restart (like
systemd: a stop you asked for is not a crash).

`/slots/{i}/start` answers `409 {"code":"component_off","component":"livetv.slots"}` while
the gate is closed.

### 2. The reconciler loop

`cmd/maktaba-server/internal/supervisor/reconcile.go`, one goroutine:

1. Read desired state: the store's `Desired(ctx, machine)` (51.02) and the revision.
2. Compare with each process's gate and status, compute the actions, and apply them in
   order (§3).
3. Write status (§6), write the cache (§4).
4. Wait for `components_changed` (lib/pq listener from `store/pqlisten`), or 30 s, then
   go to 1. Read only when the revision moved (N12).

Level-based: a missed message is caught on the next pass. A failed action (a process that
does not start) is reported and retried on the next pass; the process's own restart policy
(backoff 1–60 s, 5 restarts in 10 min, then failed) still applies under it.

### 3. Ordering

The catalog's `Order()` (51.01) gives requirements first. The reconciler starts in that order
and stops in reverse:

- **Start:** a component's processes are allowed only when every `requires` is Running on
  this machine (its probe answered). Until then the component is Waiting with
  `dependency_off:<id>` (requirement not enabled) or `RequirementsMet = unknown` (enabled,
  still starting). `any:a|b` is met by one of them.
- **Stop:** dependents first. Disabling `llm` while `classify` is enabled closes `llm`'s
  gate; `classify` keeps its wish (H3) and its LLM stages are 51.06's to stop.
- Postgres stays `First` and is not a component; the reconciler never touches it.

### 4. Before the database is up

`<data>/state/components.json`, written atomically (`layout.WriteFileAtomic`) after every
pass that changed something:

```json
{ "revision": 1842, "written_at": "2026-11-03T10:00:00Z", "machine": "home",
  "bundle": "full",
  "desired": { "llm": {"want":"enabled","generation":3},
               "transcription": {"want":"enabled","hold":{"by":"ana","until":null},"generation":9} } }
```

At boot, before Postgres answers, the supervisor sets gates from this file, so a held
transcription does not start for a minute at 3 a.m. after an update and stop again (H2). A
first boot with no file uses the bundle's defaults (Phase 1: `full`, which equals today's
config-driven table). Once the database answers, the first reconcile pass replaces the cache's
answer. A cache from a newer `revision` than the database (a restored database) loses: the
database wins.

### 5. Kill switch and things that block

- `[components] reconcile = "on" | "off"` in `server.toml` (new `ComponentsSection`, default
  on). **Off:** the supervisor builds the process table exactly as today from `server.toml`
  (the old code path, kept), still writes status with condition `reason = reconcile_off`, and
  passes `MAKTABA_COMPONENTS_RECONCILE=off` to every child so 51.05 and 51.06 runners act on
  nothing too (README §12).
- `Supported = false`: the platform is not in `platforms`, or free memory is below
  `resources.ram_mb` at start (`platform_unsupported`, `not_enough_memory`). The latter is
  checked at start only, never to stop a running process.
- `Installed = false`: the binary or venv is missing (`not_in_payload`).
- An `env_override` (51.03 §4) or `boot_flag` (`--no-mdns`) keeps the gate as the flag says
  for this boot and is reported.

### 6. Status

The supervisor writes `component_status` rows with `part = 'supervisor'` for every component
that has a process or a supervisor loop on this machine, after every pass and whenever a
process changes state (debounced to 1 s). Phase from the process status:

| Process status | Phase |
|---|---|
| gate closed, `want = installed` | Off |
| gate closed by a hold | Stopped |
| gate closed by a condition | Waiting (with reason) |
| starting, probe not yet answered | Starting |
| running, probe passing | Running |
| asked to stop, not exited | Draining |
| backoff after a crash | Starting, `detail.restarts` up |
| failed past `max_restarts` | Failed, `crash_loop` |

`/processes` gains a `component` field per process, so the desktop app (which polls it) can
group them later.

### 7. Supervisor loops

Two loops live in the supervisor and belong to switchable components: `supervisor:mdns`
(`advertiseMachine`, `discovery.mdns`) and `supervisor:backups` (`pg.RunBackups`, `backups`).
Each gets its own context from the reconciler; disabling cancels it and waits its
`stop_grace_s` (5 s). A backup in progress is never cut: today `RunBackups`
(`internal/pgbundle/backup.go:205`) passes its loop context into `Backup(ctx, …)`, so a
cancel would abort the dump. It changes to give `Backup` the server's context and check the
loop context only between dumps; the component then reports Draining until the dump ends.
Pre-update dumps are `core.updates` and never gated.

## Acceptance criteria

- **Given** `transcription` enabled with two `stt` workers running,
  **when** an admin sets it to `installed`,
  **then** both workers get SIGTERM within 2 s, have 130 s to exit, are not restarted, and the
  status goes Draining then Off; the API, streaming and the main pipeline keep their pids.
- **Given** `llm` installed, ollama in the payload,
  **when** `llm` is set to `enabled`,
  **then** ollama starts without a server restart and is Running within 15 s plus its own
  warm-up, reported apart (N3).
- **Given** `transcription` held until started,
  **when** the server restarts for an update,
  **then** no `stt` worker starts at any time during the boot (the cache says held).
- **Given** `[components] reconcile = off`,
  **when** desired state says `llm = installed` but `[llm] enabled = on`,
  **then** ollama runs (today's behaviour) and status says `reconcile_off`.
- **Given** `livetv.slots` installed,
  **when** the front door asks `/slots/green/start`,
  **then** it gets `409 component_off` and no slot starts.
- **Given** a component whose requirement is enabled but still starting,
  **when** the reconciler passes,
  **then** the dependent stays Waiting and starts within one pass after the requirement is
  Running.
- **Given** no change for 5 minutes,
  **when** the reconciler idles,
  **then** it made at most 10 queries (N12).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | fake children (`fakechild_test.go`) | `Allow(false)` on a running child | asked to stop; killed after the catalog grace, not 10 s |
| T02 | unit | child stopped by `Allow(false)` | wait 2 min | not restarted |
| T03 | unit | slot with gate closed | `Want(true)` | stays stopped; `/slots/{i}/start` → 409 `component_off` |
| T04 | unit | desired from a fake store | pass | actions in `Order()`; stop in reverse |
| T05 | unit | requirement Starting | pass | dependent Waiting; next pass after Running → allowed |
| T06 | unit | cache file says held; DB down | boot | stt never started |
| T07 | unit | cache revision 50, DB revision 40 | first pass | DB wins |
| T08 | unit | `reconcile = off` | boot | process table equals today's build for the same config (golden) |
| T09 | unit | counting fake store, fake clock | idle 5 min | ≤ 10 queries |
| T10 | unit | backups loop mid-dump | disable `backups` | dump completes; then loop returns |
| T11 | unit | status mapping table (§6) | each process state | phase as the table |
| T12 | integration | real supervisor, scratch Postgres, fake pipeline worker | flip `transcription` | worker stops/starts, others' pids unchanged |
| T13 | CI | `components-live` (51.06) | `process` scenarios | N3, N4 |

## Edge cases

- **A process with no probe** (stt workers' probe is their `/healthz`) is Running when its
  probe answers; one with none is Running after `healthy_after`.
- **ollama pulling a model** at start is warm-up: Running is reported once `/api/version`
  answers; the pull's progress goes to `detail.progress`.
- **Holds that expire** (`hold_until` passed) count as no hold at the next pass (≤ 30 s late).
- **The relay on a frozen home** stays not wanted by today's rule even when allowed.
- **A node** reads `component_desired_for(node)` with its worker login and reports through
  `component_status_report` (51.02).
- **Windows** stops children with CTRL_BREAK (`askChild`), same grace rules.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cmd/maktaba-server/internal/supervisor/reconcile.go` (new) | The loop, ordering, actions. |
| `cmd/maktaba-server/internal/supervisor/components.go` (new) | Role/instance → component mapping from the catalog; status rows. |
| `cmd/maktaba-server/internal/supervisor/compcache.go` (new) | `<data>/state/components.json` read/write. |
| `cmd/maktaba-server/internal/supervisor/run.go` | `Allow`, the `allowed` gate in the start loop, grace from the catalog. |
| `cmd/maktaba-server/internal/supervisor/state.go` | `/processes` `component` field; `/slots/{i}/start` 409. |
| `cmd/maktaba-server/internal/supervisor/supervisor.go` | `Process.Component`, `allowed` on the child. |
| `cmd/maktaba-server/internal/supervisor/env.go` | Pass `MAKTABA_COMPONENTS_RECONCILE` to children. |
| `cmd/maktaba-server/internal/config/config.go` | `ComponentsSection` (`[components] reconcile`). |
| `cmd/maktaba-server/cmd_serve.go` | Build the optional processes with closed gates; start the reconciler; gate `advertiseMachine` and `RunBackups`. |
| `cmd/maktaba-server/internal/pgbundle/backup.go` | `RunBackups`: a running dump keeps the server's context; the loop context is checked between dumps. |
| `cmd/maktaba-server/internal/supervisor/reconcile_test.go`, `compcache_test.go` (new) | T01–T11. |
| `cmd/maktaba-server/reconcile_pg_test.go` (new) | T12. |
| `cmd/maktaba-server/README.md` | Document `[components] reconcile` beside the other `server.toml` sections. |

### Tests

- Unit: T01–T11 (`go test ./cmd/maktaba-server/...`, using the existing fake children).
- Integration: T12 in the `integration` job (scratch Postgres).
- **CI:** `components-live` (defined in 51.06) runs the `process` scenarios for N3/N4/N5.
  The **update rehearsal** runs on this PR (it touches the supervisor); scenario 12 (51.03)
  must stay green.
- **No local Mac UI tests** and no runs against the live home: the reconciler is proven in CI.

### Rollout

Phase 1, dark: seeded desired state (51.03) equals today's config, so the first pass finds
nothing to change. The supervisor log shows `components_reconciled changed=0` at boot.

### Rollback

`[components] reconcile = off` restores the boot-time, config-driven table exactly (T08),
with no downgrade. A release rollback ignores the cache file and the tables.

### Dependencies

51.02 (store, listener, tables). 51.03 seeds the rows it reads; built in parallel, both land
before Phase 1 ships.
