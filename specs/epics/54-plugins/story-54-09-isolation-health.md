# Story 54.09 — Crash isolation, health, logs and quotas

> Epic 54 · Phase 1 · Depends on: 54.02, 54.07, Epic 53 (53.01, 53.02) · Needed by: 54.13

## Why

A plugin written by someone else will crash, hang, loop, or log a gigabyte a day. None of that
may take the pipeline down, hide a core failure, or leave the owner guessing which plugin is to
blame. Epic 53 gives every component its logs and health; this story gives every plugin its
own, plus a circuit breaker so a broken plugin stops itself instead of filling the failing-items
page.

## Description

### 1. Isolation, layer by layer

| Fails | Contained by | Effect |
|---|---|---|
| A plugin job traps, times out, hits memory | the job's Wasmtime store (54.02) | that job fails with its cause (54.06); the host continues |
| The host process crashes or is killed | supervisor (Epic 51 process component) | restart with backoff; in-flight jobs requeued with no attempt counted (54.06 §3) |
| The host crash-loops | Epic 51's restart limit | `plugins.host` *Failed*; every plugin *Waiting* `dependency_off:plugins.host`; core stages unaffected |
| A plugin floods the API | token bucket (54.04) | refused calls; breaker counts them |
| A plugin floods logs | host rate limit (§3) | lines dropped with a counter |
| A plugin fills its data quota | API quota | `plugin_quota`; breaker counts |

The pipeline worker never loads plugin code, so none of these can crash a worker.

### 2. The circuit breaker

Kept by the pipeline's plugin dispatcher (one place sees every result), persisted in
`plugin_health`:

- **Trip** when, for one plugin, ≥ 5 `plugin_trap`/`plugin_timeout`/`plugin_memory` in 10
  minutes, or > 50 % of its last 20 jobs failed for any plugin cause, or ≥ 50 API refusals in 10
  minutes.
- **Action:** an Epic 51 **hold** on `plugin.<id>` with reason `plugin_crash_loop` (drain, keep
  queue), an in-app notice to admins ("{plugin} stopped itself after repeated errors"), an
  Epic 53 error group.
- **Reset:** only by an admin pressing Start (the hold has no `until`). Pressing Start clears the
  window.

### 3. Logs

- The host writes each plugin's lines to stdout as Epic 53 schema lines (53.01) with
  `service = "plugin.<id>"`, `version`, `job_id`, `stage`, `level`, `code`, `fields`, and
  `origin = "plugin"`; the supervisor's capture (53.02) puts them in
  `<data>/logs/segments/plugin.<id>/…` so retention and search work as for any component.
- **Limits per plugin:** 100 lines/s burst, 10 MB per day, 8 KB per line; dropped lines are
  counted and one `log_dropped` line per minute says how many.
- **Plugin text is data.** Log fields from a plugin are stored and shown escaped; the logs page
  marks them "from plugin {name}". Problem reports (Epic 53.15) leave plugin logs out unless the
  admin ticks "Include plugin logs" per plugin (README §9 T2).
- The host's own lines use `service = "plugins.host"`.

### 4. Health

Per plugin in `component_status` (Epic 51) and `plugin_health`:

| Field | Source |
|---|---|
| phase, conditions | Epic 51 runner |
| jobs done / failed (24 h, 7 d), by cause | dispatcher |
| p50 / p95 job time, p95 memory high-water mark, fuel used | host `Result` |
| API calls and refusals (24 h) by code | API metrics |
| data used / quota, artifacts used / quota | API |
| last error code and time | dispatcher |

Metrics on the API and host `/metrics`: `maktaba_plugin_jobs_total{plugin,outcome}`,
`maktaba_plugin_job_seconds{plugin}`, `maktaba_plugin_memory_peak_bytes{plugin}`,
`maktaba_plugin_refusals_total{plugin,code}`, `maktaba_plugin_log_dropped_total{plugin}`.
Epic 53's dashboards gain a Plugins row (53.12).

### 5. Status mapping (Epic 53 §10)

`plugins.host`'s status mapping: probe failing → "Plugins are paused" banner for admins only;
no user-facing feature stops (plugin outputs already written stay).

## Acceptance criteria

- **Given** a plugin that traps on every job,
  **when** 5 jobs fail within 10 minutes,
  **then** the plugin is held with `plugin_crash_loop`, admins get one notice, its remaining
  jobs stay queued, and core stages keep running.
- **Given** the host is killed 10 times in a minute,
  **when** Epic 51's restart limit is reached,
  **then** `plugins.host` is *Failed*, every plugin is *Waiting* with
  `dependency_off:plugins.host`, and transcription, thumbnails and playback are unaffected.
- **Given** a plugin logging 10,000 lines per second,
  **when** a minute passes,
  **then** ≤ 100/s are kept, a `log_dropped` line reports the rest, and other components' logs
  are not delayed.
- **Given** a problem report sent by an admin,
  **when** "Include plugin logs" is not ticked,
  **then** the report contains no `plugin.*` lines and no `plugin_data`.
- **Given** a held plugin,
  **when** the admin presses Start,
  **then** the breaker window resets and jobs resume.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | integration (PG, py) | fake host trapping | 5 jobs | hold + notice + error group |
| T02 | integration (PG, py) | 20 jobs, 11 failed | dispatch | hold |
| T03 | integration (go, supervisor) | kill host repeatedly | supervise | Failed; plugins Waiting; core claims continue |
| T04 | integration (rust + go) | log flood | 60 s | rate limit, `log_dropped`, other services unaffected |
| T05 | unit (go) | problem report builder | with / without tick | plugin lines included / excluded |
| T06 | unit (web) | logs page | plugin lines | escaped, labelled |
| T07 | integration | metrics endpoints | scrape | series present with plugin label |
| T08 | CI | Epic 53 fault rehearsal (53.07) gains "plugin host crash-loop" | run | UI stays up; banner for admins |

## Edge cases

- **A plugin that fails only on some videos** (bad input): it trips the 50 % rule only when
  most jobs fail; otherwise the failing items show on the failing-items page as usual.
- **Breaker during an update**: the hold applies to the plugin component, so the new version
  starts held too; the admin sees why.
- **Clock jumps**: windows use monotonic time in the dispatcher and are rebuilt from
  `plugin_health` rows after a restart.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/plugins/breaker.py` (new) | §2. |
| `plugind/src/logging.rs` (new) | §3 limits and Epic 53 schema. |
| `api/internal/plugins/health.go` (new), metrics | §4. |
| `shared/db/migrations/NNNN_plugin_health.sql` + `.sqlite.sql` | `plugin_health`. |
| Epic 53 problem report builder | Plugin logs opt-in per plugin. |
| Epic 53 status mapping table / catalog fields | `plugins.host` mapping. |
| `web/src/i18n/{en,ar}.json` | Notice, banner, labels. |

### Tests

- Integration T01–T04, T07; unit T05, T06; CI T08 in Epic 53's fault rehearsal.

### Rollout

Dark until a plugin runs.

### Rollback

Release rollback; `plugin_health` ignored by older releases.

### Dependencies

54.02, 54.07; Epic 53 53.01 (schema) and 53.02 (capture). Before 53.02 lands, plugin lines go
to `server.log` with the same fields.
