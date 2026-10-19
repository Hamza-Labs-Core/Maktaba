# Story 22.9 — Process supervision and crash recovery

A single-host Maktaba is one `maktaba-server serve` that runs three child
processes (api, streaming, pipeline) plus the in-process web server. Today
the supervisor treats any child exit as fatal for the whole stack: it
SIGTERMs the others and returns, hoping an external service manager will
start everything again. A crash in the TV process therefore takes the API,
the pipeline and the web UI down with it, and a child that hangs (alive but
no longer answering) is never noticed at all.

As an operator I want the supervisor to watch each process on its own —
above all the streaming (live TV) process — restart only the one that
crashed or hung, keep the rest serving, and show me what happened.

This is the **single-host, process-level** half of resilience. Failing a
channel over to another host is Epic 32 story 32.4; the two are
complementary (32.4 needs a host whose processes come back on their own).

## Acceptance criteria

- AC1. **Per-child restart.** When one child exits, only that child is
  restarted; the other children and the web server keep running and keep
  their PIDs.
- AC2. **Backoff.** Restarts back off exponentially (default 1 s, doubling,
  capped at 60 s). A child that stays up for `healthy_after` (default 5 min)
  resets its backoff to the initial delay.
- AC3. **Crash-loop guard.** `max_restarts` restarts (default 5) inside
  `max_restarts_window` (default 10 min) marks the child `failed`. A failed
  child does not stop the others; the supervisor keeps retrying it slowly
  (`failed_retry`, default 5 min) and the state is visible (AC7).
- AC4. **Restart policy.** `[supervisor] restart = always | on-failure |
  never` in `server.toml` (default `always`). `on-failure` leaves a child
  that exited with code 0 stopped; `never` keeps today's behaviour (any
  child exit stops the whole stack, for installs where systemd, launchd or
  compose restarts the unit).
- AC5. **Hang detection.** Each role has a liveness probe (HTTP GET, 200 =
  alive) run every `probe_interval` (10 s) with `probe_timeout` (3 s).
  After `startup_grace` (60 s) or the first successful probe,
  `probe_failures` (3) failures in a row kill the child, and it is restarted
  through the normal backoff path with exit reason `hang`.
  - api: admin port `/healthz` (`[server] admin_addr`, default `:9100`).
  - streaming: admin port `/healthz` (`:9101`).
  - pipeline: logs port `/healthz` (`127.0.0.1:9102`).
- AC6. **Streaming self-health.** The streaming `/healthz` returns 503 when
  its watchdog heartbeat stops advancing. The heartbeat advances only when
  the live-channel engine lock and registry can be taken. `/readyz` also
  reports whether the database is reachable. A slow or missing database
  makes streaming *not ready*; it does not get it killed.
- AC7. **Visibility.** The supervisor serves its state on a loopback admin
  endpoint (`[supervisor] admin_addr`, default `127.0.0.1:9103`):
  for each child its state, pid, restarts, last exit (code, signal, reason,
  uptime), last restart time, uptime and last probe, plus recent events.
  The API serves the same data, for admins only, at
  `GET /api/system/processes`, and the web System Health page shows a
  "Processes" card per child (status chip, restarts, last crash reason) in
  English and Arabic.
- AC8. **After the fact.** Every crash and hang is (a) written to
  `<data_dir>/supervisor/events.jsonl` (survives a supervisor restart and
  is reloaded on boot) and (b) recorded by the API as a `health_samples`
  row (`proc.<role>.crash` / `proc.<role>.hang`, value 1), so it appears in
  the control centre's health history (`GET /api/queues/health`). No new
  migration.
- AC9. **Logging.** Every start, exit and restart is logged with role,
  pid, exit code, signal (POSIX), uptime and the next delay.
- AC10. **Shutdown unchanged.** SIGINT/SIGTERM drains every child exactly as
  before (graceful signal, `--shutdown-grace`, then kill). A shutdown during
  a backoff wait starts nothing new and returns promptly.
- AC11. **Both platforms.** Windows and POSIX use the same code; only
  signal names and the graceful-stop signal differ.

## Already in place (verified, not changed here)

- A live channel's ffmpeg muxer is supervised inside the station: it is
  restarted with backoff while viewers stay attached
  (`streaming/internal/channel/station.go` `supervise`).
- A channel tuned after a streaming restart joins at the wall-clock
  position: `seek = (now - block.start_at) + source_offset`
  (`streaming/internal/channel/join.go`). Nothing is replayed from the
  start.
- Stale `channel_runtime` rows for this host are cleared at streaming boot
  by `feat/tv-core` (migration 0117 + engine heartbeat). This story depends
  on that work and does not duplicate it.

## Test cases

- TC1. Crash: a fake child exits 3; only it gets a new PID, the sibling's
  PID is unchanged, restarts = 1, last exit code 3.
- TC2. Hang: a fake child stops answering its probe; after
  `probe_failures` failures it is killed and restarted, reason `hang`.
- TC3. Crash loop: a child that exits at once reaches `failed` after
  `max_restarts` in the window. The sibling keeps running, and the gaps
  between restarts grow.
- TC4. Shutdown during backoff returns within the grace and starts no new
  process.
- TC5. `on-failure` + exit 0 leaves the child stopped; `never` + any exit
  stops the stack (legacy).
- TC6. Config: defaults when `[supervisor]` is absent; bad values are
  rejected with the key name.
- TC7. Streaming liveness: a wedged engine probe flips `/healthz` to 503
  after the stale window; a healthy one keeps it 200.
- TC8. API: `GET /api/system/processes` is 403 for non-admins, proxies the
  supervisor JSON for admins, and returns `available:false` when the
  supervisor cannot be reached. The recorder writes one sample per crash
  event, idempotently.
- TC9. Web: the Processes card renders state chips, restarts and the last
  crash reason; it shows an "unavailable" note when the supervisor is not
  reachable.

## Edge cases

- EC1. A binary that cannot start at all (missing, permission) counts as a
  crash and goes through backoff into `failed`. It does not abort the stack.
- EC2. The api runs migrations before binding its admin port: until the
  first successful probe, probe failures only count after `startup_grace`.
- EC3. Grandchildren (ffmpeg under streaming) are not in a process group
  the supervisor kills. An orphaned muxer exits when its stdin pipe closes.
  A Windows job object is a follow-up.
- EC4. Single-role mode (`--role streaming` in a container): the same
  policy applies; set `restart = "never"` to leave restarts to the
  orchestrator.
