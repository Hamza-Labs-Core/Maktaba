# Story 53.08 — One request id from the app to the last process

> Epic 53 · Phase 2 · Depends on: 53.01 · Needed by: 53.11, 53.15

## Why

The API makes a request id (`api/internal/middleware/requestid.go`, UUIDv7) and sends it to the
pipeline as gRPC metadata `maktaba-request-id`, which the pipeline never reads. No app sends one,
so a user's "it failed at 10:15" can't be tied to a server line. Most API handler and background
lines use plain `slog.*` and carry no id. A problem report (53.15) is only useful if its request
ids lead to the server's lines.

## Description

### 1. Clients make the id

Every app sends `X-Request-Id: <UUIDv7>` on every call to the home, one per user action (a page
load and its parallel calls share one; a retry keeps it). The app writes it in its own log ring
(53.01) and keeps the **last 20** with their route pattern and result for reports.

| App | Where |
|---|---|
| Web | `web/src/lib/api.ts` (and the streaming URLs as a `rid` query parameter only for HLS playlists, which can't carry headers from every player) |
| Apple | `MaktabaKit/APIClient.swift` (`sendRaw`), `StreamProxy.swift` |
| Android | `core/api/ApiClient.kt` (OkHttp interceptor) |
| Android TV | `tv/data/TvApi.kt` |
| Desktop shell | the Tauri commands that call the server |
| Cloud web | `web/cloud/src/api.ts` |

### 2. The servers carry it

- **API:** accepts a valid UUIDv7 from the client (else mints one, as today); returns it in the
  response header; every handler line uses the context logger. A lint (`tools/lint-log`) flags
  `slog.Info/Warn/Error(` without `Context` inside `api/internal/handlers/**`. Background work
  started by a request (a scan, a job enqueue) copies the id into the job's payload as
  `origin_request_id`.
- **Pipeline:** reads `maktaba-request-id` from gRPC metadata and binds it (`bind_request_id`,
  which has no callers today); a job claimed from the queue binds `job_id` and its
  `origin_request_id`.
- **Streaming:** takes `X-Request-Id` (or `rid` on playlists) and logs it with the session id it
  opens, so `stream_session_opened` → `vod_transcode_exited` → `playback_failed` share it.
- **Relay:** the cloud relay passes the header through unchanged and logs it with `home_h`
  (53.17), so a remote failure can be found on both sides.
- **OTLP on:** a `traceparent` is made from the same id (the first 16 bytes) so traces and logs
  join (Epic 21.3).

### 3. Users can quote it

Error screens already show an error id for some failures; they now show a short reference: the
first 8 characters of the request id ("Reference: 0193f2a1"), selectable, and "Copy details"
copies the full id and the time. Support and the admin's Logs page (53.11) accept the short form.

| Key | English | Arabic |
|---|---|---|
| `error.reference` | Reference: {ref} | المرجع: {ref} |
| `error.copyDetails` | Copy details | نسخ التفاصيل |

## Acceptance criteria

- **Given** the web app loads a library page,
  **when** its calls reach the API,
  **then** every call carries the same `X-Request-Id`, and the API's lines for them carry it.
- **Given** a request that starts a pipeline job,
  **when** the job runs,
  **then** its lines carry `job_id` and `origin_request_id` equal to the request's id.
- **Given** a playback that fails,
  **when** the admin searches the id,
  **then** the API's, streaming's and the player's `playback_failed` lines all come up.
- **Given** a client sends a malformed id,
  **then** the API mints a new one and logs `request_id_replaced`.
- **Given** an error screen,
  **then** it shows "Reference: xxxxxxxx" in English or Arabic.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (web, Swift, Kotlin ×2) | mock server | make calls in one action | one id, header present |
| T02 | unit (Go) | request with id | handler logs | id on every line |
| T03 | unit (Python) | gRPC call with metadata | handler logs | `request_id` bound |
| T04 | integration | API + pipeline + streaming | play a file that fails | one id across the three |
| T05 | lint (CI) | handler with bare `slog.Info(` | lint | fails |

## Edge cases

- **A client sends the same id for unrelated actions** (a bug): harmless; search shows both.
- **Ids in URLs** (`rid` on playlists) are not secrets; they are kept out of the cache key.

## Build plan

### Files to touch

See §1 and §2, plus `api/internal/middleware/requestid.go`, `pipeline/src/maktaba_pipeline/grpc/` (the
server interceptor), `streaming/internal/httpx/reqlog.go`, `cloud/internal/relay/`, `tools/lint-log/`.

### Tests

T01–T05 in each component's unit job.

### Rollout

Servers first (they accept the header), apps in the next release.

### Rollback

Revert per component; ids are optional everywhere.

### Dependencies

53.01. Needed by 53.11, 53.15.
