# Implementation Plan — Story 22.9 Process supervision and crash recovery

> Companion to [story-22-09-process-supervision.md](story-22-09-process-supervision.md).
> The story says *what* and *why*. This plan says *how*.
> Single-host and process-level only. Failing a channel over to another
> host is Epic 32 story 32.4.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Restart loop | `cmd/maktaba-server/internal/supervisor/run.go`: one goroutine per child. Nothing about a child's exit touches the other children. |
| Settings | `[supervisor]` in `server.toml` (`config/supervisor.go`), parsed and validated by `supervisor.PolicyFromConfig`. Defaults are listed in §3. |
| Hang detection | The supervisor sends an HTTP GET to each role's `/healthz`. Failures count after `startup_grace`, or straight away once a probe has succeeded. `probe_failures` failures in a row kill the child, and the exit is recorded with reason `hang`. |
| Streaming self-health | New `streaming/internal/watchdog`. Its hard probe is `channel.Engine.Responsive`, which takes the engine lock and the registry lock (new file `channel/liveness.go`). Its soft probe is a database ping. `/healthz` is `health.CheckedLive` (new in `shared/health/go/livez.go`). |
| Pipeline self-health | `/healthz` on the existing logs port (thread-served). An asyncio task beats every 5 s (`log/liveness.py`), and the endpoint returns 503 when the last beat is older than 300 s (`MAKTABA_PIPELINE_LOOP_STALE`). |
| Visibility | The supervisor serves `GET /processes` on loopback (`admin_addr`, default `127.0.0.1:9103`). The api reads it from `MAKTABA_SUPERVISOR_URL` and proxies it to `GET /api/system/processes` for admins only (`handlers/processes`). The web System Health page has a Processes card. |
| After the fact | `<data_dir>/supervisor/events.jsonl` (rotates at 1 MiB, reloaded at boot). The api's `Recorder` copies crash and hang exits into `health_samples`. |
| Migrations | None. `health_samples` (slot 0089) already exists on both backends. |
| Out of scope | Cross-host failover (Epic 32.4). Killing the grandchildren of a child, such as ffmpeg (EC3). Clearing stale `channel_runtime` rows at boot, which `feat/tv-core` does (migration 0117 + `Engine.ClearStaleRuntime`). |

## 1. Architecture

```
maktaba-server serve
 ├─ web (in-process)
 ├─ supervisor admin  127.0.0.1:9103  GET /processes ──┐
 └─ Supervisor.Run                                     │ proxied by
     ├─ loop(api)        probe :9100/healthz           │ GET /api/system/processes
     ├─ loop(streaming)  probe :9101/healthz ◄─ watchdog heartbeat
     │                                          (engine lock, registry)
     └─ loop(pipeline)   probe :9102/healthz ◄─ asyncio heartbeat
                                                  │
 events.jsonl ◄── emit(started|exited|restarting|failed|hung)
                                                  │
 api Recorder ── poll /processes ── INSERT health_samples proc.<role>.crash|hang
```

### Child loop (per role)

```
start (a fresh exec.Cmd cloned from the template)
  ├─ the start fails → exit{reason=start_failed}
  └─ running: the probe goroutine checks /healthz every probe_interval
        └─ probe_failures failures in a row → killReason=hang, Kill
wait → exit{code, signal, uptime, reason=crash|clean|hang|shutdown}
if shutting down          → state=stopped, return
restart=never             → state=exited, Run returns the exit (legacy)
on-failure and clean      → state=exited, return
uptime ≥ healthy_after    → reset backoff, forget earlier exits
exits in window ≥ max_restarts → state=failed, delay=failed_retry
otherwise                 → state=backoff, delay=backoff, backoff*=2 (capped)
wait for the delay (a shutdown cancels the wait) → restarts++ → start again
```

Shutdown: `Run` sets `stopping` under the lock, cancels the loops (backoff
waits end and probes stop), sends the graceful signal to every running
child (SIGTERM on POSIX, Kill on Windows, as before), and kills them after
`--shutdown-grace`.

### Liveness, by role

| Role | Probe | Fails when |
|---|---|---|
| api | `http://127.0.0.1:<admin_addr port>/healthz` | the HTTP server stops answering (the process is wedged) |
| streaming | `http://127.0.0.1:9101/healthz` | the watchdog heartbeat is older than 45 s (`MAKTABA_WATCHDOG_STALE`), which means the engine lock or registry was unavailable at every beat in that window, or the beat loop itself is stuck |
| pipeline | `http://127.0.0.1:9102/healthz` | the asyncio loop has not beaten for 300 s |

A database outage is *readiness*: streaming `/readyz` reports `db`, and
`/healthz` stays 200. A restart would not fix the database, so killing
the process for it would only add a crash loop to the outage.

## 2. Files

| Path | Change |
|---|---|
| `cmd/maktaba-server/internal/config/supervisor.go` | new: `SupervisorSection` + `DefaultSupervisor()` |
| `cmd/maktaba-server/internal/config/config.go` | `Supervisor` field + default |
| `cmd/maktaba-server/internal/supervisor/policy.go` | new: `Policy`, `PolicyFromConfig`, `nextBackoff` |
| `cmd/maktaba-server/internal/supervisor/run.go` | new: `Supervisor` (`New`, `Run`, the loop, the probe, events); package `Run` kept as a wrapper |
| `cmd/maktaba-server/internal/supervisor/state.go` | new: states, reasons, `Snapshot`, `Handler` |
| `cmd/maktaba-server/internal/supervisor/eventlog.go` | new: `EventSink`, `FileSink` (JSONL + rotation) |
| `cmd/maktaba-server/internal/supervisor/supervisor.go` | `Process.Probe`, `ProbeURL`, `cloneCmd`; the old `Run` is removed |
| `cmd/maktaba-server/internal/supervisor/env.go` | `MAKTABA_SUPERVISOR_URL` for the api, `StateURL` |
| `cmd/maktaba-server/internal/supervisor/signal_*.go` | `signalName` (POSIX names the signal; Windows has none) |
| `cmd/maktaba-server/cmd_serve.go` | builds the policy, the file sink and the admin listener |
| `shared/health/go/livez.go` | new: `CheckedLive`, `AdminMuxWithLiveness` |
| `streaming/internal/watchdog/` | new package |
| `streaming/internal/channel/liveness.go` | new: `Engine.Responsive` (no edits to files `feat/tv-core` touches) |
| `streaming/watchdog_wire.go`, `streaming/main.go` | wiring (3 small hunks) |
| `pipeline/src/maktaba_pipeline/log/liveness.py`, `log/http.py`, `__main__.py` | heartbeat + `/healthz` |
| `api/internal/handlers/processes/` | new: proxy handler + `Recorder` |
| `api/internal/router/processes.go`, `api/main.go` | mount + recorder start |
| `web/src/pages/Admin/Processes.tsx` (+ test), `SystemHealth.tsx`, `i18n/{en,ar}.json` | Processes card |
| `deploy/server.toml.example`, `deploy/packaging/etc/maktaba-server.toml` | documented `[supervisor]` block |

## 3. Settings (`[supervisor]`)

| Key | Default | Meaning |
|---|---|---|
| `restart` | `always` | `always`, `on-failure` or `never` |
| `backoff_initial` / `backoff_max` | `1s` / `60s` | exponential restart delay |
| `healthy_after` | `5m` | uptime after which the backoff and the crash history reset |
| `max_restarts` / `max_restarts_window` | `5` / `10m` | exits inside the window that make a child `failed` |
| `failed_retry` | `5m` | retry interval while a child is `failed` |
| `probe_interval` / `probe_timeout` | `10s` / `3s` | liveness polling |
| `probe_failures` | `3` | failures in a row that kill the child |
| `startup_grace` | `60s` | failures before the first success do not count until this has passed |
| `admin_addr` | `127.0.0.1:9103` | state endpoint; `off` disables it |

Environment knobs in the children: `MAKTABA_WATCHDOG_STALE` (streaming,
default 45 s) and `MAKTABA_PIPELINE_LOOP_STALE` (pipeline, seconds, default
300).

## 4. API contract

`GET /api/system/processes` (admin) returns `200`:

```json
{
  "available": true,
  "host": "tv-box",
  "started_at": "…",
  "policy": {"restart": "always", "max_restarts": 5, "…": "…"},
  "processes": [
    {"role": "streaming", "state": "backoff", "pid": 0, "restarts": 2,
     "crash_loop": false, "uptime_sec": 0,
     "last_exit": {"at": "…", "pid": 4120, "code": 2, "signal": "", "reason": "crash",
                   "error": "exit status 2", "uptime_sec": 12.4},
     "last_restart_at": "…", "next_restart_at": "…",
     "last_probe": {"at": "…", "ok": true, "consecutive_failures": 0},
     "probe_url": "http://127.0.0.1:9101/healthz"}
  ],
  "events": [{"seq": 9, "at": "…", "role": "streaming", "kind": "exited",
              "reason": "crash", "code": 2, "uptime_sec": 12.4}]
}
```

Without a supervisor the response is
`{"available": false, "reason": "…", "processes": [], "events": []}`.
A non-admin gets 403.

`health_samples` rows: `(at = event time, host = api hostname,
metric = proc.<role>.crash | proc.<role>.hang, value = 1)`, inserted with
`ON CONFLICT DO NOTHING`, so a restarted api that sees the same events
again writes nothing new.

## 5. Tests

| Test | Covers |
|---|---|
| `supervisor/run_test.go` `TestCrashRestartsOnlyThatChild` | TC1: a fake child (the test binary in `MAKTABA_FAKE_CHILD=serve` mode) exits 3 on `/crash`; the sibling's PID is unchanged |
| `TestHangIsKilledAndRestarted` | TC2: `/hang` blocks `/healthz`; the child is killed, restarted, and has reason `hang` |
| `TestCrashLoopBacksOffThenFails` | TC3: growing delays, `failed`, `crash_loop`, the sibling undisturbed |
| `TestShutdownDuringBackoff` | TC4 |
| `TestOnFailureLeavesCleanExitStopped`, `TestNeverStopsTheStack` | TC5 |
| `TestStartFailureIsACrash`, `TestHealthyRunResetsBackoff`, `TestEventsPersistAcrossSupervisorRestart`, `TestFileSinkRotates`, `TestHandlerServesSnapshot` | EC1, AC2, AC8, AC7 |
| `signal_unix_test.go` / `supervisor_windows_test.go` | AC11: SIGKILL is recorded as a signal on POSIX; TerminateProcess shows as code 1 on Windows |
| `supervisor/policy_test.go` | TC6 |
| `shared/health/go/livez_test.go`, `streaming/internal/watchdog`, `channel/liveness_test.go`, `streaming/watchdog_wire_test.go` | TC7 |
| `pipeline/tests/test_log_liveness.py` | pipeline `/healthz` (the HTTP and event-loop cases are integration-marked because they open sockets) |
| `api/internal/handlers/processes/processes_test.go` | TC8 (SQLite `health_samples`) |
| `web/src/pages/Admin/Processes.test.tsx` | TC9 |

The process tests spawn real children and take seconds, which is over the
unit tier's per-test cap. They skip under `-short` and run in the
integration tier (`go test -tags=integration ./...` without `-short`).

## 6. Rollout

- The defaults change behaviour on purpose: a crash no longer stops the
  stack. Installs that rely on systemd `Restart=` set `restart = "never"`
  to keep the old behaviour.
- The packaged systemd unit and launchd plist keep working unchanged. The
  supervisor exits only on a signal (or with `never`), so the unit's own
  restart policy now only covers the supervisor itself.
- Depends on `feat/tv-core` for clearing stale `channel_runtime` rows at
  streaming boot. Until it lands, a restarted streaming process can leave
  rows that the next tune overwrites (`SetRuntime` upserts).
