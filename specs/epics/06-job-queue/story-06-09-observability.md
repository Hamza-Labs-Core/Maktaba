# Story 6.9 — Observability hooks

## Description

Operators need to see what the queue is doing without grepping logs.

## Acceptance criteria

- `GET /api/queue/stats` (contract owned here, implementation in API)
  returns:
  ```json
  {
    "by_stage": {
      "transcribe": {"pending": 12, "running": 1, "paused": 3,
                     "failed": 0, "done": 184},
      ...
    },
    "by_state": {"pending": 22, "running": 3, ...},
    "eta_total_sec": 31738.4,
    "realtime_factor_p50": 0.31
  }
  ```
- A Prometheus-compatible `/metrics` endpoint emits, at minimum:
  - `maktaba_jobs_total{stage,state}` gauge.
  - `maktaba_job_attempts_total{stage,outcome}` counter.
  - `maktaba_job_duration_seconds{stage,outcome}` histogram.
  - `maktaba_job_realtime_factor{stage}` summary.
- Structured logs (`structlog` JSON, architecture §11/§12.5) include
  `{job_id, video_id, stage, state, attempts}` on every state-changing
  event.

## Test cases

- `test_stats_aggregates_correctly` — fixture jobs across stages and
  states → stats match.
- `test_metrics_include_all_required_keys` — scrape `/metrics`; assert
  each metric and label is present.
- `test_log_event_for_state_transition` — capture logs during a full
  job lifecycle; assert `transition_to_running`, `paused_for_user`,
  `transition_to_done` are present with required fields.

## Edge cases

- **Empty queue.** Stats return zeros; metrics still emitted (so
  alerting on "no jobs running for X minutes" works).
- **A long-failing job creating noisy retry logs.** Logged at WARN
  with backoff window; debounce via the same `not_before` to avoid
  log spam.
