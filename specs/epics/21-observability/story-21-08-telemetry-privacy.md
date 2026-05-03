# Story 21.8 — Privacy of telemetry

Every observability surface protects user data; opt-in is real.

## Acceptance criteria

- AC1. No telemetry leaves the host by default. Tracing, error
  webhooks, and external integrations are all explicitly opt-in via
  config, with a top-level `[telemetry].outbound_enabled = false`
  master switch.
- AC2. A canonical "redaction list" enumerates field types that are
  never logged, traced, or webhooked: passwords, API keys, JWT
  bearer values, file contents, full filesystem paths, transcript
  body text. Enforced by a CI lint over log/trace call sites.
- AC3. Logs include a "telemetry-leak detector" test that scans 1,000
  representative log lines for known-sensitive substrings (a list of
  test secrets) and fails on any match.
- AC4. The web client's optional `POST /api/telemetry/web-vitals` is
  off-by-default and labeled clearly in the privacy section of the
  UI.

## Test cases

- TC1. Default-off: a fresh install with default config makes no
  outbound DNS queries beyond NTP and TLS for the configured public
  origin (verified by a packet-capture integration test).
- TC2. Leak scan: a deliberate `slog.Info("password=" + p)` is caught
  by the redaction lint and fails CI.
- TC3. Redaction at runtime: a misbehaving call site that escapes the
  lint is caught at runtime by a structured-log middleware that
  rewrites known-sensitive keys to `***`.

## Edge cases

- EC1. Config-stored secret echoed back through `/api/settings` —
  forbidden; setting endpoints return only metadata, never the value.
- EC2. Stack trace containing a user file path — paths under the
  media root are masked to `<media>/<library>/<relative>` before
  emission.
- EC3. Browser console in a developer-mode build — verbose logs are
  off in production builds; a flag is required to opt in.
