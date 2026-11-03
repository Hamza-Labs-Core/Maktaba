# Story 53.01 — One log line for every part

> Epic 53 · Phase 1 · Depends on: — · Needed by: 53.02, 53.08, 53.14, 53.17

## Why

The Go services and the pipeline already share a field contract (`shared/log/go/logger.go`,
`maktaba_pipeline/log`), but the cloud writes its own plain JSON, the apps write free text to
`os_log` and logcat, the user id is logged raw, and the redaction rules live in four copies.
A log keeper (53.02), an index (53.09) and error groups (53.10) need one shape they can trust.

## Description

### 1. The line

One JSON object per line (NDJSON), UTF-8, ≤ 64 KB (longer `msg`/`err` are cut with `…`, as the
Go logger does at 60 KB today).

| Field | Type | Required | Notes |
|---|---|---|---|
| `ts` | RFC 3339 UTC, ms | yes | |
| `level` | `debug` `info` `warn` `error` `fatal` | yes | `fatal` = the process is about to exit (panic, uncaught exception) |
| `service` | string | yes | the component: `api`, `streaming`, `pipeline`, `supervisor`, `logs`, `postgres`, `desktop`, `web`, `apple.player`, `apple.remote`, `apple.tv`, `android.player`, `android.remote`, `android.tv`, `cloud.api`, `cloud.relay`, `cloud.worker`, `cloudlink` |
| `msg` | snake_case event name | yes | a constant, never runtime data (`make lint-log` already enforces this in Go; the same lint joins Python) |
| `version`, `env` | string | yes (servers) | as today |
| `node` | string | yes (servers) | the machine id (home or joined machine; cloud node A/B/W) |
| `request_id` | UUIDv7 | when known | 53.08 |
| `trace_id`, `span_id` | hex | when OTLP is on | W3C |
| `session_id`, `job_id`, `video_id`, `stage`, `channel_id` | string | when known | as today |
| `user_h` | 16 hex | when known | HMAC-SHA256(user id, home log key), §3 |
| `home_h` | 16 hex | cloud only | HMAC-SHA256(server id, cloud log key) |
| `err.kind`, `err.code`, `err.msg` | string | on failures | `kind` from the existing error kinds (`unreachable`, `timeout`, `db`, …); `code` a stable slug; `msg` scrubbed |
| `err.stack` | array of frames | `error`/`fatal` | ≤ 30 frames, own frames marked `"own": true` |
| `error_id` | UUIDv7 | `error`/`fatal` | `shared/errrpt` mints it; shown to users on error screens |
| `app`, `app_version`, `platform`, `os_version`, `device_model`, `install_id` | string | clients | `install_id` random per install, never a hardware id |
| `attrs` | object | no | anything else; flat keys, ≤ 50 keys |

`raw_user_id`, `email`, `name`, IPs and tokens are **never** fields.

The schema lives in `shared/log/schema/line.schema.json` with golden examples in
`shared/log/schema/examples/*.ndjson`.

### 2. One redaction list

`shared/redact/rules.json` (the `list.yaml` story 21.1 planned, as JSON so every language reads
it without a YAML parser):

- **keys** (substring, case-insensitive): `password`, `passwd`, `secret`, `token`, `api_key`,
  `apikey`, `auth`, `authorization`, `cookie`, `credentials`, `private_key`, `privatekey`,
  `bearer`, `dsn`, `signature`, `sig`;
- **values** (regex): JWTs, `Bearer …`, URL userinfo, query values of `token|key|sig|signature|X-Plex-Token`,
  `postgres(ql)?://…@`, emails, card-number shapes (Luhn-checked);
- **IPs:** v4 last octet zeroed, v6 last 80 bits zeroed (`192.168.1.0`, `fd00:1:2::`), except
  loopback;
- **paths:** `/Users/<name>/`, `/home/<name>/`, `C:\Users\<name>\` → `~/`;
- **export-only** (applied when anything leaves the home, 53.16): media paths under a library
  root → `<library>/…`, titles → removed.

Each logger (Go `shared/log/go/redact.go`, Python `maktaba_pipeline/log`, Swift `Log.swift`,
Kotlin `Diagnostics.kt`, TypeScript `web/src/lib/errorLog.ts`, cloud) loads the same rules
(embedded at build time) and passes the same table of cases in
`shared/redact/cases.json`.

### 3. Hashed user ids

The home makes a 32-byte **log key** at first start (`<data>/secrets/log.key`, mode 0600, not in
the database, not in backups that leave the home). `user_h = hex(HMAC-SHA256(key, user_id))[:16]`.
The Go context logger, the pipeline's `bind_user_id` and the API's request logger write `user_h`
instead of `user_id`. The admin UI resolves `user_h` to a name by asking the API, which hashes
its user list (≤ a few hundred users: cached). The cloud does the same with its own key for
`home_h`.

Old lines in rings and in `server.log` still carry `user_id` until they age out; the index
(53.09) hashes `user_id` on ingest if it sees one.

### 4. Clients

- **Apple:** `Log.swift` keeps `os.Logger` (Console.app, `log stream` still work) and also
  appends the same event, as a 53.01 line, to a 1 MB ring file in Application Support
  (`Logs/app.ndjson`, two files of 512 KB). This is what reports (53.15) attach.
- **Android:** `AppLog` writes the same line to `files/logs/app.ndjson` (two files of 512 KB),
  besides logcat.
- **Web:** `errorLog.ts` keeps a 2,000-line in-memory ring of 53.01 lines (console, API
  failures, route changes as `route_changed` with the route pattern, not the URL).
- **Desktop (Tauri):** `applog.rs` writes `app.log` lines in the schema (`service: desktop`).
- **Cloud:** `cloud/cmd/*` call `mlog.Init` from `shared/log/go` instead of building their own
  handlers (53.17 does the rest).

## Acceptance criteria

- **Given** a line from each of Go, Python, Swift, Kotlin, TypeScript and the Rust shell,
  **when** the conformance test validates it,
  **then** it passes `line.schema.json`.
- **Given** a log call with `token=abc` in a URL, an email in an error, and `/Users/mahmoud/` in a path,
  **when** the line is written in any language,
  **then** it shows `token=[REDACTED]`, `[EMAIL]` and `~/`.
- **Given** a request by user 42,
  **when** the API logs it,
  **then** the line has `user_h` and no `user_id`, and the same user always gets the same `user_h` on this home.
- **Given** two homes,
  **when** the same user id is hashed on each,
  **then** the hashes differ.
- **Given** the cloud starts,
  **when** it logs,
  **then** its lines carry `service`, `version`, `env`, `node` like the home's.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (each language) | `shared/redact/cases.json` | run every case | each output equals the expected |
| T02 | unit (each language) | `shared/log/schema/examples/` | validate | all pass; the bad examples fail |
| T03 | unit (Go) | context with user id | log a line | `user_h` present, `user_id` absent |
| T04 | unit (Go, Python) | the same key and id | hash in both | equal |
| T05 | lint (CI) | Python sources | `lint-log` rule | f-strings and `%` in `msg` fail |
| T06 | unit (Apple, Android) | write 3,000 lines | read the ring file | ≤ 1 MB, newest kept |

## Edge cases

- **A third-party library logs free text** (ffmpeg, Postgres): 53.02 wraps such lines as
  `msg: "raw_line"` with the text in `attrs.text` (scrubbed), so they still fit.
- **The log key is lost** (a restore onto a new machine): a new key is made; old `user_h`s no
  longer resolve to names. The admin UI shows "unknown user". Acceptable.
- **Over-redaction** (a video titled "Token of love"): titles are not scrubbed at home; only the
  key and value rules above.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/log/schema/line.schema.json`, `examples/` (new) | §1. |
| `shared/redact/rules.json`, `cases.json` (new) | §2. |
| `shared/log/go/redact.go`, `logger.go`, `ctx.go` | Load the shared rules (embed); `user_h`; `node`; `err.*` fields. |
| `shared/log/go/userhash.go` (new) | HMAC, key loading. |
| `api/internal/middleware/slog_logger.go` | `user_h` instead of `user_id`. |
| `cmd/maktaba-server/cmd_setup.go` (the setup step that makes the home's other keys) | Make `log.key` at first start. |
| `pipeline/src/maktaba_pipeline/log/__init__.py`, `redact.py` | Shared rules, `user_h`. |
| `pipeline/tools/lint_log.py` (new) + `pyproject.toml` | The Python `lint-log`. |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/Log.swift`, `LogRing.swift` (new) | Ring file, rules. |
| `apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/diagnostics/Diagnostics.kt`, `LogRing.kt` (new) | Ring file, rules. |
| `web/src/lib/errorLog.ts` | Ring of lines, rules. |
| `apps/desktop/src-tauri/src/applog.rs` | Schema lines. |
| `cloud/cmd/maktaba-cloud/main.go`, `cloud/cmd/maktaba-cloudlink/main.go` | `mlog.Init`. |
| `tools/log-schema/check.py` (new) | Validates golden lines from every language in CI. |

### Tests

T01–T06 in each language's unit job. `tools/log-schema/check.py` runs in the `_unit.yml` job
that already runs for `shared/**`.

### Rollout

Additive fields first; `user_id` → `user_h` in one release with the index's ingest hash (53.09).

### Rollback

Revert; the old fields come back. Lines written meanwhile still parse (the schema is a superset).

### Dependencies

None. Needed by 53.02, 53.08, 53.14, 53.17.
