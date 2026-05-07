# Story 7.20 — Health, version, metrics

`/api/system/health`, `/api/system/version` (§9.7), plus optional
OpenTelemetry export (§2.1).

**AC-1 — Health composition.**
- **Given** the API needs DB, Pipeline gRPC, and Streaming gRPC,
- **When** `GET /api/system/health` is called,
- **Then** the response is `{status: "ok"|"degraded"|"down",
  components: {db: ..., pipeline: ..., streaming: ...}, checked_at}` and
  the HTTP status reflects the worst component (`200` ok/degraded, `503`
  down — applied only when the API is itself reachable; if Postgres is
  fully down the API listener still binds and serves health, returning
  503 with `db: "down"`).

**AC-2 — Version endpoint.**
- **Given** the binary is built with `-ldflags "-X main.version=..."`,
- **When** `GET /api/system/version` is called,
- **Then** the response is `{version, build_sha, build_time, go_version,
  schema_revision}`.

**AC-3 — Metrics export.**
- **Given** `[telemetry].enabled = true`,
- **When** the API runs,
- **Then** `/metrics` (separate port `metrics_listen`) exposes Prometheus
  metrics including `http_requests_total`, `http_request_duration_seconds`,
  `grpc_client_calls_total`, `ws_active_connections`,
  `db_pool_in_use`, `db_pool_idle`, `job_queue_pending` per stage.

**AC-4 — OTel traces opt-in.**
- **Given** `[telemetry].otel_endpoint` is set,
- **When** any HTTP or gRPC call is made,
- **Then** spans are emitted with consistent service name `maktaba-api`,
  attributes include `http.route`, `http.status_code`, `db.statement`
  (truncated), and parent context is propagated via W3C `traceparent`.

**Test cases:**
- Integration: kill Pipeline → `/health` reports `pipeline: "down"` and
  the overall status `degraded` (Streaming and DB still up).
- Integration: a process that holds the API process up but blocks DB
  queries → `/health` returns 503 with `db: "down"` (the listener stays
  up because the API caches its own health-evaluation pool separately
  from the application connection pool).
- Integration: `/metrics` is not authenticated by default (assumed
  bound to localhost), but is configurable to require an admin token.
- Integration: a single request appears as one span tree spanning API +
  Pipeline.

**Edge cases:**
- Health-check storm from a misconfigured Kubernetes liveness probe —
  the health endpoint is cached 1 s to avoid hammering Pipeline gRPC. Test
  case: 100 health calls in 1 s → only 1 gRPC call to Pipeline.
- Schema revision mismatch (binary expects v15, DB is at v14) — `/health`
  reports `db: degraded` with `detail: "schema-behind"`; the binary still
  serves read-only requests but blocks writes that would need v15. Test
  case: skip a migration → /health is degraded; PATCH fails with 503
  `type: schema-out-of-date`.
