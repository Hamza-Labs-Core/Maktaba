# Story 21.4 — Health and readiness probes

Every service has `/healthz` (liveness) and `/readyz` (readiness) and a
unified `/api/system/health` aggregating across services for the UI.

## Acceptance criteria

- AC1. `/healthz` returns 200 when the process is alive; never blocks
  on dependencies. Used by the orchestrator (compose / launchd /
  systemd) to restart hung processes.
- AC2. `/readyz` returns 200 only when:
  - DB connection pool has ≥ 1 healthy conn.
  - Required gRPC peers (API → Pipeline; API → Streaming) are reachable.
  - Configured caches have been warmed (or are explicitly degraded).
  Returns 503 with a JSON body listing failing dependencies.
- AC3. `/api/system/health` aggregates all three services'
  `/readyz` plus disk free, queue depth, transcribe budget remaining,
  and renders a JSON the web admin panel can display.
- AC4. Probes are unauthenticated, bound to a separate admin port by
  default (or fronted by a reverse proxy with allowlist).

## Test cases

- TC1. Liveness: kill Postgres; `/healthz` still returns 200,
  `/readyz` returns 503. Note: when the API process itself becomes
  unable to accept TCP connections (e.g., total OOM, not the
  primary-DB outage scenario), neither probe responds — that is a
  liveness probe failure for the orchestrator and is tested
  separately.
- TC2. Aggregator: with Pipeline down, `/api/system/health` returns
  `degraded` with `pipeline.reason="grpc_unavailable"`.
- TC3. Cold start: during the first 30 s after start, `/readyz` may
  return 503 with `reason="warming"`; the probe never deadlocks.

## Edge cases

- EC1. DB primary failover — `/readyz` flips 503 → 200 within 30 s as
  the connection pool reconnects.
- EC2. Streaming with all replicas down — `/api/system/health` shows
  `streaming.unavailable` and the UI surfaces "playback offline" while
  search and library still work.
- EC3. SQLite mode: the Postgres-specific dependencies are absent and
  `/readyz` doesn't check them; documented per-mode probe matrix.
