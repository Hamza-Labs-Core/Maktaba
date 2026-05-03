# Story 10.14 — Secret loading and redaction

§11.5: secrets only in env or config file, never in the DB. Never logged,
never returned by `/api/settings`.

**AC-1 — Env-var precedence.**
- **Given** a secret defined in both `[auth].jwt_private_key_env` and a
  TOML file,
- **When** loaded,
- **Then** the env-var value wins; the file-based value is logged as
  ignored.

**AC-2 — Logger redaction.**
- **Given** a struct with a `secret:"true"` tag on a field,
- **When** logged via `slog`,
- **Then** the field is rendered as `<redacted>`. Per-handler request
  logging strips known-sensitive header values (`Authorization`, `Cookie`,
  `X-Maktaba-CSRF`).

**AC-3 — `/api/settings` redaction.**
- **Given** any GET on settings (Epic 7 Story 7.15 AC-1),
- **When** rendered,
- **Then** every secret-bearing key is `"<redacted>"` and a sibling
  `*_present` boolean indicates whether the secret is configured.

**AC-4 — gRPC metadata stripping.**
- **Given** a gRPC call between API and Streaming/Pipeline,
- **When** logged or traced,
- **Then** any `authorization` or `*-token` metadata is redacted in
  server-side logs and OTel attributes.

**Test cases:**
- Unit: a struct with a tagged secret field round-trips to `<redacted>`
  in slog output.
- Integration: `/api/settings` response contains no plaintext secrets
  (assert via regex on the body).
- Integration: an `Authorization` header in a request is not present in
  the request log.

**Edge cases:**
- A user-defined config key that happens to look like a secret (`my_token`)
  but isn't tagged — defaults to redacted by name match (`token`,
  `secret`, `key`, `password`) unless explicitly opted-out via a
  `notsecret:"true"` tag.
- A URL containing a token in the query string (e.g. signed URLs in
  logs) — the request logger replaces `?sig=...` with `?sig=<redacted>`.
