# Story 23.4 — Secrets management

Secrets live in env or config files only; never in DB rows users can
read; never in logs; never in metrics; never in error reports.

## Acceptance criteria

- AC1. Canonical secret list is enumerated in architecture §11.5:
  `MAKTABA_ADMIN_TOKEN`, `MAKTABA_DATABASE_URL`,
  `MAKTABA_JWT_PRIVATE_KEY_PEM`, `MAKTABA_JWT_PUBLIC_KEY_PEM`,
  `OPENAI_API_KEY`, etc. Each has a documented owner service.
- AC2. The Streaming service never sees the JWT private key or any
  STT backend keys (architecture §11.5); the binary is shipped with
  no code path that reads them.
- AC3. `/api/settings` never returns secret values, only metadata
  (key name, whether set, source: env/file). Secret values are
  write-only.
- AC4. A redaction middleware rewrites known secret-shaped values
  (high-entropy strings, keys named `*_KEY`, `*_TOKEN`, `*_PASSWORD`)
  in any log line that escapes the structured-field rule.

## Test cases

- TC1. Streaming binary: `strings` on the binary contains no
  reference to the JWT private key env name; static analysis CI
  asserts.
- TC2. Settings round-trip: `GET /api/settings` for a configured
  `OPENAI_API_KEY` returns `{configured: true, source: "env"}`,
  never the value.
- TC3. Redaction: a `slog.Info` with a secret-shaped value writes
  `***` to the log; the original value never appears in any sink.

## Edge cases

- EC1. Secret in a stack trace — middleware redacts the secret in the
  trace before emission.
- EC2. Multi-line PEM key in env — supported; parsing tolerates
  `\n`-escaped and literal-newline forms.
- EC3. Secret rotation while in flight — the service holds the loaded
  value for the lifetime of an in-flight request and reloads on next
  inbound after a SIGHUP or admin endpoint trigger.
