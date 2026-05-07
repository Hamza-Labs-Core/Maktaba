# Epic 21 — Observability

> **Status:** spec + plans complete. **Source:** `specs/epics/21-observability/`.
> **Anchors:** [`architecture.md`](../../../specs/architecture.md) §5 (observability platform), §9 (service correlation).

## Goal

A self-hoster can answer "what's it doing?" and "why is it slow?" without attaching a debugger. Every request, job, and stream is traceable end-to-end. Health is reportable at a glance. Alerting is optional but supported. **Personal data and secrets never appear in logs or traces.** This epic does not specify a monitoring vendor — it specifies the *surfaces* (metrics, logs, traces, health) and the cardinality, format, and retention rules that work with Prometheus, OpenTelemetry, or a plain text-log fallback.

## Stories & Plans

| # | Story | Plan | Summary |
|---|-------|------|---------|
| 21.1 | [Structured logging](../../../specs/epics/21-observability/story-21-01-structured-logging.md) | [plan-21-01](../../../specs/epics/21-observability/plan-21-01-structured-logging.md) | JSON logs in prod (Go slog, Python structlog); required base fields; runtime level toggle (`SIGUSR1` / admin endpoint). |
| 21.2 | [Metrics surface](../../../specs/epics/21-observability/story-21-02-metrics-surface.md) | [plan-21-02](../../../specs/epics/21-observability/plan-21-02-metrics-surface.md) | Per-service `/metrics` (9100/9101/9102); strict cardinality (no per-row labels); native histograms with fallback. |
| 21.3 | [Distributed tracing](../../../specs/epics/21-observability/story-21-03-distributed-tracing.md) | [plan-21-03](../../../specs/epics/21-observability/plan-21-03-distributed-tracing.md) | OTel across web → API → gRPC → pipeline/streaming → DB; W3C `traceparent`; head sampler (100 % error/slow, 1 % otherwise). |
| 21.4 | [Health & readiness probes](../../../specs/epics/21-observability/story-21-04-health-readiness-probes.md) | [plan-21-04](../../../specs/epics/21-observability/plan-21-04-health-readiness-probes.md) | `/healthz` (liveness), `/readyz` (readiness), `/api/system/health` aggregator; admin port localhost-only by default. |
| 21.5 | [Error reporting & alerting](../../../specs/epics/21-observability/story-21-05-error-reporting.md) | [plan-21-05](../../../specs/epics/21-observability/plan-21-05-error-reporting.md) | UUIDv7 `error_id`, category (auth/db/ffmpeg/network/ml/unknown); rate-limited webhook; opt-in Sentry/Honeycomb. |
| 21.6 | [Audit log](../../../specs/epics/21-observability/story-21-06-audit-log.md) | [plan-21-06](../../../specs/epics/21-observability/plan-21-06-audit-log.md) | Append-only `audit_log` (monthly partitions); file mirror; trigger-enforced immutability; per-category retention. |
| 21.7 | [Job & pipeline visibility](../../../specs/epics/21-observability/story-21-07-job-pipeline-visibility.md) | [plan-21-07](../../../specs/epics/21-observability/plan-21-07-job-pipeline-visibility.md) | Extends Epic 7 endpoints; `GET /api/queue/stats`, `GET /api/jobs/{id}`, `WS /ws/jobs?job_id=`. |
| 21.8 | [Telemetry privacy](../../../specs/epics/21-observability/story-21-08-telemetry-privacy.md) | [plan-21-08](../../../specs/epics/21-observability/plan-21-08-telemetry-privacy.md) | Default-off outbound; canonical redaction list; CI lint over call sites; runtime middleware redaction; web-vitals opt-in only. |

## Cross-cutting decisions

- **Required base log fields.** Every line: `ts` (RFC 3339 UTC), `level`, `service`, `msg`. Contextual injection via context: `request_id`, `session_id`, `job_id`, `video_id`, `user_id`.
- **Log levels.** `debug` (off in prod), `info` (default), `warn`, `error`, `fatal`. Runtime toggle via `SIGUSR1` (Go) or admin endpoint.
- **Metrics ports.** API 9100, Streaming 9101, Pipeline 9102. Localhost-only by default; opt-in public exposure requires bearer auth.
- **Metrics baseline.** `*_request_duration_seconds`, `*_in_flight_requests`, `db_query_duration_seconds`, `cache_hits/misses_total`, `pipeline_jobs_total`, `transcode_active_sessions`, `pipeline_stage_duration_seconds`.
- **Histogram buckets.** Exponential native (Prometheus 2.40+) or fixed fallback `[1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000]` ms.
- **Trace propagation.** W3C `traceparent` (no B3). Head sampler: 100 % for errors and slow spans (wall-clock > route's p95 budget), 1 % otherwise. Query strings hashed (never stored verbatim).
- **Health probe semantics.** `/healthz` = liveness (always 200 if alive, never blocks). `/readyz` = readiness (DB conn, gRPC peers, cache warmed; ≤800 ms cumulative). Warm-up period ≤30 s post-start.
- **Audit log categories.** `auth` (login, token use), `library` (lifecycle), `admin` (user ops, ACL), `data` (export/purge/delete), `config` (settings change), `keys` (JWT rotation). Monthly partitions; retention 6 m (auth) to 36 m (keys) default.
- **Audit append-only.** Trigger blocks UPDATE / DELETE. Retention via DETACH PARTITION + DROP TABLE; every partition drop is audited as a `data` row.
- **Error reporting.** `error_id` UUIDv7 generated at first emission and propagated via gRPC metadata header `x-error-id`. Webhook rate-limit 10/min token bucket; circuit breaker after 5 consecutive failures, closes after 60 s. Redacted payloads (no paths, no sensitive fields).
- **Redaction list.** Canonical `shared/redact/list.yaml`. CI lint forbids logging known-sensitive field names. Runtime middleware rewrites known keys to `***`.
- **Default off.** Tracing OTLP, error webhooks, Sentry, web vitals all disabled by default. Master switch `[telemetry].outbound_enabled = false`.

## API endpoints introduced

- `POST /admin/log/level` (story 21.1)
- `/metrics` per service (story 21.2)
- `/healthz`, `/readyz`, `/api/system/health` (story 21.4)
- `GET /api/libraries/{id}/audit`, `GET /api/security/audit` (story 21.6)
- `GET /api/queue/stats`, `GET /api/jobs/{id}`, `WS /ws/jobs` (story 21.7)
- `POST /api/telemetry/web-vitals` (opt-in, rate-limited) (story 21.8)

## Migrations claimed

| Slot | Plan | Tables |
|---|---|---|
| (one slot) | plan-21-06 | `audit_log` (partitioned on `occurred_at`, monthly); enforcement triggers; `audit_log_partition_*` child tables. |

## Files & code paths

- `shared/log/{go,py,ts}/`
- `shared/metrics/go/{registry,http,histograms,lint/cardinality_lint}.go`
- `shared/tracing/{go,py}/`, `web/src/lib/tracing.ts`
- `shared/health/go/{healthz,readyz,checks}.go`, `api/internal/system/health_aggregator.go`
- `shared/errrpt/{go,py}/`
- `shared/audit/go/{emit,mirror,replayer,partitions}.go`
- `api/internal/handlers/{queue_stats,job_detail,ws_jobs}.go`
- `shared/redact/{list.yaml,go/redactor,go/path_masker,go/slog_handler,go/lint/log_lint}`

## Dependencies

- Story 21.1 is foundational; loggers consumed by every other story.
- Story 21.2 metrics consumed by perf-CI dashboards and Epic 23 alerting.
- Story 21.3 tracing depends on context propagation (21.1).
- Story 21.5 error reporting depends on `audit_log` (21.6) for cross-service correlation.
- Story 21.6 audit log foundational for [Epic 23 — Security](epic-23-security.md) auditing.
- Story 21.7 extends Epic 7 endpoints; depends on indexes (Story 18.7), `audit_log` (21.6), error fields (21.5).
- Story 21.8 wraps every outbound surface (logger, tracer, webhook, Sentry).

## Out of scope

- Specific vendor integrations beyond OpenTelemetry, Sentry-compatible APIs, generic webhooks.
- Custom dashboard UIs (web client owns admin panel rendering).
- Alert routing & escalation logic (opted in, not built-in).
- Cross-cluster distributed tracing (single-host focus for v1).
- Custom metric types beyond Prometheus standard (counter, gauge, histogram, summary).

## See also

- [Epic 23 — Security](epic-23-security.md) (audit log consumption).
- [Epic 16 — Subscriptions](epic-16-subscriptions.md) (telemetry sink).
- [Glossary](../glossary.md) — structured logging, log level, cardinality, native histogram, trace, span, head sampler, liveness, readiness, error_id, error category, audit log, redaction, path masking, web vitals.
