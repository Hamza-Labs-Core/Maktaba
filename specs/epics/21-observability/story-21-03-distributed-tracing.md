# Story 21.3 — Distributed tracing

OpenTelemetry traces span the full request path: client → API → gRPC →
Pipeline / Streaming → Postgres / FFmpeg / ChromaDB.

## Acceptance criteria

- AC1. Each service is wired with `otelhttp` (Go) /
  `opentelemetry-instrumentation` (Python) and propagates W3C
  `traceparent` headers across REST, GraphQL, gRPC, and Postgres.
- AC2. Tracing is sampled: 100 % for `error`-tagged spans, 100 % for
  any request that took > p95 budget, and 1 % otherwise. The sampler
  is a head sampler with tail-sampling-aware tags; no separate tail
  sampler required for v1.
- AC3. The web client emits a span per top-level page load and per
  search query, with the `traceparent` carried into the API call so
  the trace covers client + server.
- AC4. Tracing is opt-in: it is disabled by default; enabled via
  `[telemetry].otlp_endpoint` in the service config; never silently
  exfiltrates data.

## Test cases

- TC1. End-to-end: a search request from the web client produces a
  trace containing spans from `web → api → pipeline.Embed → postgres
  query → chroma query`.
- TC2. Sampling: 1,000 fast, successful requests produce ≈ 10 traces; a
  single slow request produces 1 trace tagged `slow=true`.
- TC3. Disabled-by-default: a fresh install produces no OTLP
  connections; `netstat`/`lsof` confirms no outbound calls.

## Edge cases

- EC1. OTLP endpoint unreachable — exporter buffers ≤ 10 MiB then
  drops with a `warn`-level log, never blocks the request path.
- EC2. Large request body — body content is never put in span
  attributes; only sizes and counts.
- EC3. PII in URLs (search query string with a personal name) — the
  query string is hashed in the span attribute, not stored verbatim.
