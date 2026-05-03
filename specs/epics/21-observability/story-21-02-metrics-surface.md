# Story 21.2 — Metrics surface

Prometheus-compatible metrics per service, with strict cardinality and
clear semantics.

## Acceptance criteria

- AC1. Each service exposes `/metrics` (default port 9100, 9101, 9102)
  with the following baseline:
  - `*_request_duration_seconds` histogram with labels `method`,
    `route_template` (never raw path), `status_class`.
  - `*_in_flight_requests` gauge.
  - `db_query_duration_seconds` histogram with `query_name` label.
  - `cache_hits_total`, `cache_misses_total` per cache.
  - `pipeline_jobs_total` counter with `stage`, `result` labels.
  - `transcode_active_sessions`, `transcode_queue_depth` gauges
    (streaming).
  - `pipeline_stage_duration_seconds` histogram per stage.
- AC2. Label cardinality is bounded: never include `video_id`,
  `user_id`, or anything per-row in a label. A static lint enforces
  this against the metric registration code.
- AC3. Histograms use exponential native buckets (Prometheus 2.40+) or
  fall back to a documented fixed bucket layout `[1ms, 2.5ms, 5ms, 10,
  25, 50, 100, 250, 500, 1000, 2500, 5000, 10000]` ms.
- AC4. `/metrics` is unauthenticated by default but bound to localhost;
  an admin can opt into network exposure via config and then it requires
  the admin token.

## Test cases

- TC1. Cardinality: a lint that scans metric registrations rejects a
  label named `id`, `path`, or anything containing user-supplied data.
- TC2. Schema test: every documented metric is present in `/metrics`
  output on a freshly-started service.
- TC3. Network exposure: with the default config, `/metrics` is
  reachable from `127.0.0.1` only; with the opt-in flag, it requires
  bearer auth.

## Edge cases

- EC1. Long-running FFmpeg subprocess on a dropped client — its
  contribution to `transcode_active_sessions` decrements only when the
  reaper claims the session, not when the parent goroutine exits.
- EC2. Restart resets counters — Prometheus handles this correctly with
  `rate()`; documentation calls it out for naive consumers.
- EC3. Web client metrics — the browser doesn't push metrics; instead
  it sends an opt-in `POST /api/telemetry/web-vitals` (defined in
  Epic 7) capped at 1 request per 5 minutes per session.
