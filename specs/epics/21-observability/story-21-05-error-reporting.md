# Story 21.5 — Error reporting and alerting integration

A self-hoster opting into alerting must get accurate signals. We do not
auto-page; we expose hooks.

## Acceptance criteria

- AC1. Every `error`-level log emits a structured `error_id` (UUIDv7),
  the stack trace (Go: `errors.WithStack` or runtime stack; Python:
  `traceback`), and a `category` (auth, db, ffmpeg, network, ml,
  unknown).
- AC2. A built-in webhook posts a redacted error summary to a
  configurable URL (Slack, Discord, generic webhook); rate-limited to
  10/min with an exponential-backoff suppress window.
- AC3. Sentry / Honeycomb / GlitchTip integration is opt-in via config;
  DSN is read from env, never logged.
- AC4. Errors crossing service boundaries carry their `error_id` so the
  upstream and downstream logs can be correlated.

## Test cases

- TC1. Burst suppression: emit 1,000 errors in 5 s; the webhook
  receives at most 10 with a "910 suppressed" summary appended.
- TC2. Cross-service correlation: a Pipeline error during transcribe
  surfaces with the same `error_id` in the API job-status row and the
  client-visible error.
- TC3. Redaction: the webhook payload omits paths, file names, and any
  field tagged `sensitive=true`.

## Edge cases

- EC1. Webhook endpoint flapping (502 on every other call) — circuit
  breaker opens after 5 consecutive failures; opens close after 60 s.
- EC2. Sentry DSN typo — the SDK does not crash the app; it logs a
  one-time warning.
- EC3. Errors during shutdown — flushed to the webhook with a 5 s
  drain budget, then dropped to local file (`error_drop_log`).
