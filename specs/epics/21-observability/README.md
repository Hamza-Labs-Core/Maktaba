# Epic 21 — Observability

**Goal.** A self-hoster can answer "what's it doing?" and "why is it
slow?" without attaching a debugger. Every request, job, and stream is
traceable end-to-end. Health is reportable at a glance. Alerting is
optional but supported. Personal data and secrets never appear in logs
or traces.

This epic does not specify a monitoring vendor; it specifies the
*surfaces* (metrics, logs, traces, health) and the cardinality, format,
and retention rules that work with Prometheus, OpenTelemetry, or a
plain text-log fallback.

## Stories

- [Story 21.1 — Structured logging](story-21-01-structured-logging.md)
- [Story 21.2 — Metrics surface](story-21-02-metrics-surface.md)
- [Story 21.3 — Distributed tracing](story-21-03-distributed-tracing.md)
- [Story 21.4 — Health and readiness probes](story-21-04-health-readiness-probes.md)
- [Story 21.5 — Error reporting and alerting integration](story-21-05-error-reporting.md)
- [Story 21.6 — Audit log for sensitive actions](story-21-06-audit-log.md)
- [Story 21.7 — Job and pipeline visibility](story-21-07-job-pipeline-visibility.md)
- [Story 21.8 — Privacy of telemetry](story-21-08-telemetry-privacy.md)
