# Story 21.1 — Structured logging

One global logger per service; all logs are structured key-value, not
free-form strings. Logs are the lowest-friction observability surface
and the one a self-hoster always has.

## Acceptance criteria

- AC1. Go services use `slog` with JSON handler in production and a
  human handler in dev; Python uses `structlog` with the same JSON
  format in production. TypeScript browser logs use a thin `logger`
  with the same field names.
- AC2. Every log line includes: `ts` (RFC 3339 UTC), `level`, `service`,
  `msg`, and (where applicable) `request_id`, `session_id`, `job_id`,
  `video_id`, `user_id`. No log lines without `service`.
- AC3. Log levels: `debug` (off in prod), `info` (default), `warn`
  (recoverable issue), `error` (operation failed), `fatal` (process
  exits). Level is configurable at runtime via signal (Go: `SIGUSR1`
  cycles level) or admin endpoint.
- AC4. No string concatenation of user data into the `msg` field;
  user-controlled strings go in their own fields with an explicit name.

## Test cases

- TC1. Schema check: a CI lint parses every log call site; a call with
  a non-fielded user-controlled value (e.g., `slog.Info("user " + name)`)
  fails the build.
- TC2. Round-trip: every emitted JSON line round-trips through `jq` and
  contains the required fields.
- TC3. Hot-reload: `SIGUSR1` to the API process toggles between info
  and debug; observed in subsequent log lines.

## Edge cases

- EC1. Log line > 64 KiB — truncated to 60 KiB with a `truncated: true`
  field; large bodies (full HTTP requests) go to the trace, not the log.
- EC2. Logs from FFmpeg subprocess (stderr) — wrapped in
  `event=ffmpeg_stderr` lines, not passed through unstructured.
- EC3. Unicode bidi text in `msg` — the JSON escaper handles RTL
  characters without garbling.
