# Epics 7–10 — API, Streaming, Library, Auth

> Story-level breakdown of the four service-shaped epics that sit between the
> Pipeline (Epics 1–6, separate doc) and the clients. Each story is sized for
> a single PR and carries Given/When/Then acceptance criteria, the test cases
> that prove the criteria, and the edge cases the implementer must keep in
> mind. Source-of-truth references are to [`specs/architecture.md`](../architecture.md)
> sections (e.g. §9.4) — this doc is *what to build* and *how to verify it*,
> not *why we made the choice*.

---

## Conventions used in this document

- **AC** = Acceptance Criteria (Given / When / Then). A story is only
  "done" when every AC has at least one passing test.
- **Test case** = a concrete unit / integration / e2e check, written as a
  one-liner the implementer can paste into a test description.
- **Edge case** = a known failure mode the implementer must consciously
  handle and have a test for. Edge cases also carry one-line GWT or a
  test-case description.
- **Endpoints** are written `METHOD /path` and link back to the architecture
  section that owns them.
- All times are UTC; all string IDs are UUID v7; all monetary numbers are
  ISO-4217 strings; all language codes are ISO 639-1.
- "The API" = the Go API Service binary (§1.2). "Streaming" = the Go
  Streaming Service binary. "Pipeline" = the Python Pipeline Service.
- Stories are independently deployable behind a feature flag unless noted
  ("blocked by"). Feature flags are read from `app_settings` (UI-editable)
  with a code default that matches the production rollout target.

---

## Epic 7 — API Server

The Go API Service is every request that isn't a media byte: library CRUD,
search, job control, settings, watch state, real-time WebSocket fan-out,
auth issuance, and the gRPC client surface to Pipeline and Streaming. It is
stateless behind Postgres and one binary scales horizontally without any
session affinity (§1.2, §10.3).

This epic covers the REST surface (§9.1–9.7), the GraphQL schema (§9 intro),
WebSocket fan-out (§9.5, §7.10), the inter-service gRPC clients (§9.9), and
the cross-cutting concerns every request inherits: pagination, error format,
validation, request limits, observability. Auth issuance (login, JWT,
cookies, refresh) lives in **Epic 10**; this epic only consumes the
middleware Epic 10 produces.

**Out of scope for Epic 7:** the GraphQL client codegen on the web side
(client epic), the Streaming Service binary (Epic 8), filesystem watching
(Pipeline / Epic 9), and authentication flow itself (Epic 10).

### Story map

| #     | Story                                                | Depends on |
|-------|------------------------------------------------------|------------|
| 7.1   | HTTP server skeleton, problem+json, request IDs      | —          |
| 7.2   | Cursor pagination primitive                          | 7.1        |
| 7.3   | Library CRUD endpoints                               | 7.1, 7.2   |
| 7.4   | Video listing, detail, patch, delete                 | 7.1, 7.2   |
| 7.5   | Video processing control (`/process`, `/reprocess`)  | 7.1        |
| 7.6   | Transcript window endpoint                           | 7.1, 7.2   |
| 7.7   | Subtitles & chapters read endpoints                  | 7.1        |
| 7.8   | Search API (FTS, semantic, hybrid)                   | 7.1, gRPC client |
| 7.9   | Saved searches                                       | 7.8        |
| 7.10  | Streaming session lifecycle (`POST /api/stream/sessions` etc.) | 7.1, gRPC client to Streaming, Epic 10 JWT signer |
| 7.11  | Watch progress sync (`/progress`, fan-out)           | 7.10, 7.16 |
| 7.12  | Job control endpoints (pause/resume/cancel/retry)    | 7.1        |
| 7.13  | Queue stats endpoint                                 | 7.1        |
| 7.14  | Collections, tags, speakers endpoints                | 7.1, 7.2   |
| 7.15  | Settings & system endpoints                          | 7.1, Epic 10 |
| 7.16  | WebSocket fan-out (`/ws/jobs`, `/ws/library`, `/ws/playback`) | 7.1, Postgres LISTEN |
| 7.17  | GraphQL schema + resolvers (mirrors REST domain)     | 7.3–7.15   |
| 7.18  | gRPC clients to Pipeline and Streaming               | 7.1        |
| 7.19  | Request validation, body/query limits, rate limiting | 7.1        |
| 7.20  | Health, version, metrics, observability              | 7.1        |

---

### Story 7.1 — HTTP server skeleton

Establish the chi-based HTTP server, the `application/problem+json` error
shape (RFC 9457), per-request IDs, structured logging, and graceful
shutdown. Every later story assumes this scaffold.

**AC-1 — RFC 9457 error envelope.**
- **Given** any handler that calls `httperror.Write(w, err)`,
- **When** the error is rendered,
- **Then** the response has `Content-Type: application/problem+json`, an
  HTTP status matching the error class, and a body `{type, title, status,
  detail, instance, requestId}` where `instance` is the request path and
  `requestId` is the per-request UUID v7 echoed in `X-Request-Id`.

**AC-2 — Request ID propagation.**
- **Given** an incoming request without `X-Request-Id`,
- **When** the request enters the middleware stack,
- **Then** a UUID v7 is generated, attached to the request context, echoed
  in the response `X-Request-Id` header, and included in every `slog` log
  line emitted while the request is in flight.
- **Given** an incoming request **with** `X-Request-Id` set to a syntactically
  valid UUID, **When** processed, **Then** that ID is used verbatim
  (idempotent retries from clients keep their ID).

**AC-3 — Graceful shutdown.**
- **Given** the server has in-flight requests,
- **When** `SIGTERM` is received,
- **Then** the listener stops accepting new connections, in-flight requests
  drain up to `shutdown_grace_sec` (default 30 s), and after the grace
  window any still-open connections are forcibly closed and the process
  exits 0.

**Test cases:**
- Unit: `httperror.Write` serializes a `NotFoundError` to a body with
  `status: 404` and `type: "https://maktaba.dev/problems/not-found"`.
- Unit: missing `X-Request-Id` header → middleware sets a v7 UUID; a
  malformed `X-Request-Id` → middleware overwrites with a fresh v7.
- Integration: a request panicking in a handler returns 500 with a
  problem+json body and the panic stack only goes to the log, not the body.
- Integration: `SIGTERM` while a slow request is mid-flight → request
  completes, then the server exits within `grace + 1 s`.

**Edge cases:**
- A handler calling `http.Error` directly (bypassing `problem+json`) is
  caught by a vet/lint rule (custom `analysispass`) and fails CI. Test
  case: the linter rule's golden test detects a synthesized `http.Error`
  call site.
- Two errors written by the same handler (double-write bug) — the second
  write is dropped and a warning is logged with the request ID. Test
  case: handler that calls `httperror.Write` twice → response body matches
  the first call exactly.
- A request that exceeds `shutdown_grace_sec` mid-shutdown → connection is
  closed; the request body is half-read; client sees a TCP RST. Test case:
  spawn a 10 s sleep handler, send SIGTERM with `grace = 1 s`, assert the
  client receives EOF inside 2 s.

---

### Story 7.2 — Cursor pagination primitive

Single shared pagination contract used by every list endpoint. No
`page`/`offset` ever — only opaque cursors over a `(sort_key, id)` tuple,
because library/video listings can shift under the user's feet (new
videos arrive, jobs change state).

**AC-1 — Opaque cursor encoding.**
- **Given** a list endpoint returning items sorted by `(updated_at DESC,
  id DESC)`,
- **When** the response is generated with a non-empty `next` page,
- **Then** the cursor is a base64url-encoded JSON `{u: "<RFC3339>", i:
  "<uuid>", v: 1}` with no padding, and is shorter than 128 bytes.

**AC-2 — Stable iteration under concurrent writes.**
- **Given** a paginated list with a fixed cursor,
- **When** new rows are inserted between page fetches,
- **Then** the cursor never returns a row whose `(sort_key, id)` is
  greater-than-or-equal-to the cursor's tuple (no duplicates), and
  newly-inserted rows appear on subsequent fresh listings (`?cursor=`
  omitted) but not in the resumed pagination.

**AC-3 — Limit and bounds.**
- **Given** a request with `?limit=N`,
- **When** N is in `[1, 200]`, the response returns at most N items.
- **When** N is missing, default 50 is used.
- **When** N is `<1` or `>200`, the response is `400 Bad Request` with
  problem+json `type: invalid-query-parameter`, `detail: "limit must be in [1,200]"`.

**Test cases:**
- Unit: encode/decode round-trip preserves `(updated_at, id)` exactly.
- Unit: a v2 cursor (future format) decoded by v1 code returns an error
  with `type: cursor-unsupported-version`, not a 500.
- Integration: list 1000 videos page-by-page (limit=50) → 20 pages, no
  duplicates, no skips, last page returns `next: null`.
- Integration: while paginating, insert a new video — the new video does
  **not** appear in the in-flight pagination but **does** appear when the
  client restarts pagination.
- Integration: request with `?cursor=garbage` returns 400 problem+json
  `type: invalid-cursor`.

**Edge cases:**
- Two rows with identical `updated_at` (microsecond collision) — the
  secondary sort by `id` (UUID v7, also time-ordered) breaks the tie
  deterministically. Test case: insert two rows with `now()` inside one
  transaction → both appear, in `id`-descending order, no infinite loop.
- A cursor whose pointed-at row was deleted between requests — the next
  page is computed as "everything strictly less than the cursor's tuple",
  so a deleted row is silently skipped. Test case: paginate, delete the
  first row of page 2, fetch page 2 → returns the rows that *would* have
  followed the deleted row, no error.
- A row whose `updated_at` was rewritten backwards by a manual SQL fix
  during pagination — the row is silently skipped or duplicated; this is
  acceptable but documented in the handler comment. Test case: out of
  scope (manual repair only).

---

### Story 7.3 — Library CRUD

Endpoints from §9.1: `GET/POST/PATCH/DELETE /api/libraries`,
`POST /api/libraries/{id}/scan`, `GET /api/libraries/{id}/stats`.

**AC-1 — Create library.**
- **Given** a request `POST /api/libraries` with body `{name, roots,
  settings}` where `name` is unique and every `roots[i]` is an absolute
  path that exists and is readable by the API process,
- **When** the request is processed,
- **Then** a row is inserted into `libraries`, the response is `201
  Created` with the full library object including `id` and `created_at`,
  and the `Location` header points to `/api/libraries/{id}`.

**AC-2 — Reject invalid roots.**
- **Given** a request whose `roots` contains a relative path, a path that
  does not exist, or a path the API process cannot read,
- **When** processed,
- **Then** the response is `422 Unprocessable Entity` with problem+json
  `type: library-roots-invalid` and `detail` listing each offending path
  with its specific failure (`not-absolute`, `not-found`,
  `not-readable`).

**AC-3 — Update and merge settings.**
- **Given** an existing library with `settings: {stt: {backend:
  "whisper-mlx"}}`,
- **When** `PATCH /api/libraries/{id}` is sent with body `{settings: {stt:
  {model: "large-v3"}}}`,
- **Then** the stored settings become `{stt: {backend: "whisper-mlx",
  model: "large-v3"}}` (deep merge), `updated_at` advances, and the
  response is the new full library object.

**AC-4 — Delete with purge flag.**
- **Given** a library with N videos,
- **When** `DELETE /api/libraries/{id}?purge=false` (default) is sent,
- **Then** the library row is deleted, all videos are unlinked (foreign
  key `ON DELETE CASCADE` removes them from the catalog), but no files on
  disk are touched.
- **When** `?purge=true` is sent, the on-disk files are deleted from each
  root **after** the DB rows are removed and the response only returns
  `204 No Content` once all deletions complete.

**AC-5 — Trigger scan.**
- **Given** an existing library,
- **When** `POST /api/libraries/{id}/scan` is sent,
- **Then** a `scan` job is enqueued at priority 50 (user-initiated), the
  response is `202 Accepted` with body `{job_id}`, and a Postgres NOTIFY
  fires on `channel = "jobs.new"`.

**AC-6 — Stats accuracy.**
- **Given** a library with mixed-state videos,
- **When** `GET /api/libraries/{id}/stats` is sent,
- **Then** the response includes `{total_videos, total_duration_sec,
  by_state: {discovered, probed, transcribed, ready, failed},
  processed_pct, by_language}`, all derived from a single SQL query
  joining `videos` + `processing_jobs`.

**Test cases:**
- Unit: deep-merge of `settings` preserves nested keys not mentioned in the
  patch.
- Integration: create library with two roots, one absolute and one relative
  → 422 listing the relative one.
- Integration: rename uniqueness — POST a second library with the same
  `name` returns 409 `type: library-name-exists`.
- Integration: `DELETE ?purge=true` against a library whose root is a
  read-only mount → DB delete succeeds, file delete returns 207 `Partial`
  with `failed_paths` listed.
- Integration: stats query stays under 50 ms on a 50,000-video library
  (perf test, gated to CI nightly only).
- Integration: after `/scan`, polling `GET /api/jobs?stage=scan` shows the
  job within 1 s.

**Edge cases:**
- `roots` contains the same path twice — dedup before insert; if any
  duplicate survives validation it is silently collapsed and a debug log
  is emitted. Test case: POST `roots: ["/a", "/a"]` → stored as `["/a"]`,
  no error.
- `roots` overlaps with another library's roots (one library nested inside
  another) — reject with 422 `type: library-roots-overlap`. Test case:
  library A has `/mnt/media`, POST B with `/mnt/media/lectures` → 422.
- Library deleted while a scan job is mid-flight — the worker checks
  `videos.library_id` exists before each insert; on FK violation it marks
  the job `cancelled` with reason `library-deleted`. Test case:
  integration test that deletes the library while the scan worker is
  paused mid-loop.
- `purge=true` on Linux NFS mount where unlink is allowed but rmdir is
  not — the per-file deletion succeeds, but the leftover directory tree
  remains; status is 207 with the directory paths listed.
- Two concurrent `POST /scan` calls — the second returns `200 OK` with
  the existing in-flight job (idempotent), not a duplicate enqueue. Keyed
  by `(library_id, stage="scan", state IN ('pending','running'))`.

---

### Story 7.4 — Video list, detail, patch, delete

Endpoints from §9.2: `GET /api/videos`, `GET /api/videos/{id}`,
`PATCH /api/videos/{id}`, `DELETE /api/videos/{id}`.

**AC-1 — List with filters.**
- **Given** a corpus of videos across multiple libraries,
- **When** `GET /api/videos?library={id}&language=ar&type=lecture&tag=tafsir
  &q=foo&sort=updated_at&limit=20` is sent,
- **Then** the response contains only videos matching every filter,
  fewer-than-or-equal-to 20 items, sorted by `updated_at DESC`, with a
  `next` cursor following the §7.2 contract.

**AC-2 — Detail view.**
- **Given** an existing video,
- **When** `GET /api/videos/{id}` is sent,
- **Then** the response includes the video row plus eagerly-joined
  `media_info`, `audio_tracks`, `chapters`, `tags`, the latest
  `transcripts.id` (if any), and `playback_state` for the requesting
  user. No transcript segments — those are paginated via story 7.6.

**AC-3 — Patch user-editable fields only.**
- **Given** a video,
- **When** `PATCH /api/videos/{id}` is sent with `{title, description,
  tags}` (any subset),
- **Then** those fields are updated; any other field in the body is
  silently ignored (no error). Tags use a delta semantic via story 7.14
  (`{add, remove}`); a flat `tags` array on PATCH replaces the set.

**AC-4 — Delete options.**
- **Given** a video,
- **When** `DELETE /api/videos/{id}` (default `?purge=false`) is sent,
- **Then** the catalog row is removed (cascading derived data) but the
  source file on disk is untouched.
- **When** `?purge=true`, the source file is unlinked and an audit row is
  written to `library_audit (action="purge", path, by_user, at)`.

**Test cases:**
- Unit: `?language=ar` filter generates `WHERE detected_language = 'ar'`,
  parameterized.
- Unit: unknown `?sort=` value returns 400 `type: invalid-sort`.
- Integration: full-text `?q=` filter routes to FTS5 if the DB is SQLite,
  to `tsvector @@ plainto_tsquery` if Postgres.
- Integration: detail view does N+1-free joins (verify with a query
  counter — exactly one round trip).
- Integration: PATCH with `{state: "ready"}` is silently ignored (state
  is owned by the pipeline, not the API).
- Integration: DELETE with `?purge=true` against a missing file
  (already deleted out-of-band) returns `204` with a warning header
  `Maktaba-Warning: file-not-found`.

**Edge cases:**
- Video that exists in DB but file missing on disk (drive unmounted) — the
  detail view still returns successfully with `media_info` from the cached
  probe; a `playable: false` flag is set in the response. Test case:
  unmount the drive, GET detail → 200 with `playable: false`.
- Two videos sharing a `content_hash` (impossible in steady state, but
  possible mid-merge) — list endpoint returns both; detail endpoint
  returns the one matching `id`. The deduper is part of Pipeline / Epic 9.
- PATCH that sends 64 KB of `description` — capped at 8 KB by the request
  validator (story 7.19). Test case: 16 KB body → 413 `type:
  payload-too-large`.

---

### Story 7.5 — Video processing control

Endpoints from §9.2: `POST /api/videos/{id}/process`,
`POST /api/videos/{id}/reprocess`.

**AC-1 — Process now (priority bump).**
- **Given** a video in any state,
- **When** `POST /api/videos/{id}/process` is sent with `{stage:
  "transcribe"?, priority: 50?}`,
- **Then** an existing pending/paused job for that `(video, stage)` has
  its priority lowered to 50 (user-initiated), or — if no job exists — a
  fresh job is enqueued at the requested stage with priority 50, and the
  response is `200 OK` with the resulting job id and current state.

**AC-2 — Reprocess from stage.**
- **Given** a video already past `from_stage`,
- **When** `POST /api/videos/{id}/reprocess` is sent with `{from_stage:
  "transcribe"}`,
- **Then** the video's `state` is rolled back to the predecessor of
  `from_stage`, the existing transcript artifacts are marked `superseded`
  (not deleted — they remain queryable until the new ones land), and a
  fresh chain of jobs from `from_stage` onward is enqueued at priority
  200 (re-process default).

**Test cases:**
- Unit: `from_stage = "transcribe"` resolves the predecessor as
  `audio_extracted` against the FSM in §3.
- Integration: process-now on a `failed` job moves it back to `pending`
  and resets `attempts = 0`.
- Integration: reprocess fires Postgres NOTIFY `videos.state_changed`.
- Integration: reprocess preserves the old `transcripts` row but marks
  `superseded_at = now()`; a UI search still returns the old segments
  until the new ones are ready.
- Integration: two concurrent `/process` calls on the same video collapse
  to one job (idempotency).

**Edge cases:**
- Reprocess `from_stage = "scan"` is rejected (scan is whole-library, not
  per-video). Returns 400 `type: stage-not-per-video`.
- Reprocess on a video whose source file is missing — the job will fail
  at `probe`; the API returns 200 anyway (the worker is the source of
  truth for execution outcomes).
- Process-now bumps a job currently `running` — priority does not
  preempt; the API responds 200 with `note: "job already running"`. Test
  case: start a job, hit /process, observe no state change.
- Reprocess on a video with an in-flight transcribe job — the in-flight
  job is left to complete, the new job is enqueued *after* it, and the
  in-flight one is marked `superseded` on completion (its outputs are
  still useful as fallback if the new one fails).

---

### Story 7.6 — Transcript window endpoint

`GET /api/videos/{id}/segments?from={sec}&to={sec}` from §9.2. Used by the
player to render the transcript sidebar in real time.

**AC-1 — Return segments overlapping a time window.**
- **Given** a video with N transcript segments,
- **When** the request specifies `?from=120&to=300`,
- **Then** the response contains every segment where `start_sec < 300 AND
  end_sec > 120` (inclusive overlap), ordered by `seq` ascending, with
  `text` already bidi-isolated for safe mixed-script rendering.

**AC-2 — Default window.**
- **Given** no `from`/`to`,
- **When** the request is sent,
- **Then** the response returns the first 200 segments of the latest
  non-superseded transcript and includes a `next` cursor.

**AC-3 — Word-level optionally included.**
- **Given** `?words=true`,
- **When** the segments are returned,
- **Then** each segment includes its `words: [{seq, start_sec, end_sec,
  text, confidence}]` array if word-level timestamps were captured,
  otherwise `words: null`.

**Test cases:**
- Unit: segment-overlap predicate matches the four cases (fully inside,
  spanning start, spanning end, fully containing).
- Integration: 50-segment fixture, `?from=10&to=12.5` returns exactly the
  one segment whose `[start_sec, end_sec)` straddles 12.0.
- Integration: superseded transcript is **not** returned by default;
  `?include_superseded=true` returns it.
- Performance: 10,000-segment transcript paginated at 200/page completes
  in under 100 ms per page on a SQLite test DB.

**Edge cases:**
- Window crosses a paused-transcribe gap (segments only exist up to
  3500 s on a 5000 s video) — `?from=4000` returns an empty `items` and
  `partial: true` in the response root, instructing the UI to render
  "transcribing…" overlay.
- `from > to` — returns 400 `type: invalid-time-window`.
- `from < 0` is clamped to 0; `to > duration_sec` is clamped to
  `duration_sec` (no error). Test case: `?from=-5&to=99999` against a
  600 s video → returns segments in `[0, 600]`.
- Right-to-left text rendering: `text` field MUST be wrapped in U+2068
  FIRST STRONG ISOLATE … U+2069 POP DIRECTIONAL ISOLATE so that an
  English query result interleaved into an Arabic transcript does not
  reorder the surrounding paragraph in the UI.

---

### Story 7.7 — Subtitles & chapters read endpoints

`GET /api/videos/{id}/subtitles`, `GET /api/videos/{id}/chapters` from
§9.2. Read-only enumeration; the bytes themselves are served by Streaming
(Epic 8).

**AC-1 — Enumerate subtitles.**
- **Given** a video with one auto-generated VTT, one external SRT, and one
  embedded subtitle,
- **When** the endpoint is called,
- **Then** the response is an array of `{id, language, format, source,
  is_default, url}` where `url` is a signed Streaming URL valid for
  `subtitle_url_ttl_sec` (default 3600 s).

**AC-2 — Chapters with provenance.**
- **Given** a video whose chapters were inferred from transcript topic
  shifts,
- **When** the endpoint is called,
- **Then** each item includes `{seq, start_sec, end_sec, title, source}`
  with `source ∈ {embedded, manual, inferred}`.

**Test cases:**
- Unit: signed URL TTL reflects config; a frozen-clock test asserts
  `expires_at = now + ttl`.
- Integration: a video with no subtitles returns `[]`, not 404.
- Integration: a video with no chapters returns `[]`, not 404.
- Integration: external SRT is reported with `format: "srt"` even though
  Streaming serves a converted VTT to the player.

**Edge cases:**
- Video has subtitles in three languages but the requesting client sent
  `Accept-Language: ar` — the response order puts `ar` first; no other
  filtering. Test case: header-based ordering works for `ar`, `en`, `*`.
- Subtitle file disappeared from disk between the Pipeline writing it and
  the API serving the URL — the URL still gets minted; Streaming returns
  404 to the player at fetch time. The API endpoint does not stat files
  per request (perf).

---

### Story 7.8 — Search API (FTS, semantic, hybrid)

`POST /api/search` from §9.3, plus `GET /api/search/suggest`. Hybrid =
RRF over FTS + Chroma per §3.7.

**AC-1 — Hybrid mode is the default.**
- **Given** a request `POST /api/search` with body `{q: "الحمد لله"}` and no
  `mode`,
- **When** processed,
- **Then** the API runs FTS5 (or `tsvector @@`) and gRPC-calls Pipeline's
  `Embed` for the query, queries Chroma top-K (default K=50), fuses with
  RRF, applies user filters, and returns the response shape from §9.3
  with `took_ms` populated.

**AC-2 — FTS-only and semantic-only modes.**
- **Given** `{q, mode: "fts"}`, **Then** Chroma is not called (no embedding
  cost) and `took_ms` reflects only the FTS path.
- **Given** `{q, mode: "semantic"}`, **Then** FTS is not called.

**AC-3 — Filter shape.**
- **Given** filters `{language: ["ar"], library_id: ["…"], duration_sec:
  {gte: 1800}, speaker: ["sheikh-a"], date: {gte: "2024-01-01"}}`,
- **When** any combination is applied,
- **Then** results respect every filter and the SQL/Chroma queries push
  the filters down (no in-memory filtering except for the final dedup
  across the two sources).

**AC-4 — Suggest is fast.**
- **Given** `GET /api/search/suggest?q=الحم` (Arabic prefix),
- **When** the request is sent,
- **Then** the response is `{suggestions: [...]}` with up to 10 items, p99
  latency under 80 ms on a 100,000-segment fixture, sourced from the FTS
  prefix index only (no Chroma call).

**AC-5 — Highlight markers.**
- **Given** any FTS hit,
- **When** rendered,
- **Then** matched terms in `text` are wrapped `<mark>...</mark>`, the
  surrounding excerpt is at most 240 characters, and right-to-left text is
  bidi-isolated.

**Test cases:**
- Unit: RRF fusion with k=60 produces deterministic order on a synthetic
  pair of ranked lists.
- Unit: `mode=hybrid` with empty FTS results falls back to pure semantic
  scoring (no `NaN`/division-by-zero).
- Integration: an Arabic query containing diacritics matches segments
  without diacritics (FTS5 `unicode61 remove_diacritics 2` proven by a
  fixture).
- Integration: an English query against an Arabic transcript finds
  matches via Chroma's cross-language embeddings (cross-language fixture
  with `multilingual-e5-large`).
- Integration: search response includes `took_ms` decomposed as
  `took_ms.fts`, `took_ms.semantic`, `took_ms.fusion` for observability.
- Performance: p95 search latency under 250 ms on the 100,000-segment
  fixture.

**Edge cases:**
- `q` is empty or whitespace — return `400 invalid-query`. Test case:
  POST `{q: "   "}` → 400.
- `q` is 50,000 characters — capped at 1024 by the validator. Test case:
  oversize body → 400 with `detail: "q must be ≤1024 chars"`.
- Pipeline gRPC is down — `mode=hybrid` degrades to `mode=fts` and the
  response carries `degraded: true` with reason `embedding-unavailable`.
  Test case: kill Pipeline, run hybrid search → 200 with `degraded:
  true`.
- Chroma returns a segment id that no longer exists (deleted between
  embed and serve) — silently dropped from results. Test case: insert a
  Chroma row, delete the underlying segment, search → no error, no hit.
- A filter on `speaker: ["unknown-3"]` where the speaker was renamed
  between request and response — the filter is by `speaker_id`, not
  name; renames don't break in-flight queries.

---

### Story 7.9 — Saved searches

`POST /api/search/save`, `GET /api/search/saved` from §9.3.

**AC-1 — Save and replay.**
- **Given** a successful search,
- **When** the user POSTs `{name, query}` to `/api/search/save`,
- **Then** the row is stored in `saved_searches` keyed by `(user_id,
  name)`, and a subsequent `GET /api/search/saved` returns it.
- **When** `query` is replayed (the UI passes the stored `query` JSON to
  `POST /api/search` verbatim), the same hit set is returned, modulo
  catalog drift.

**AC-2 — Per-user namespacing.**
- **Given** two users save searches with the same `name`,
- **When** either lists,
- **Then** each only sees their own.

**Test cases:**
- Integration: name uniqueness per user; conflicting POST → 409
  `type: saved-search-name-exists`.
- Integration: deleting a user (Epic 10) cascades to delete their saved
  searches.

**Edge cases:**
- `query` JSON is forward-compat: an unknown filter key in stored JSON is
  ignored (logged at debug) on replay rather than failing 400. Test case:
  insert a saved search with `{filters: {future_key: "x", language:
  ["ar"]}}` → replay succeeds, ignoring `future_key`.
- Smart collections (Story 9.5) reuse this storage; ensure the schema
  supports both consumers without an enum split.

---

### Story 7.10 — Streaming session lifecycle

The API mints sessions and signs URLs; Streaming serves bytes (§9.4).

**AC-1 — Open session.**
- **Given** a video and a request `POST /api/stream/sessions` with
  `{video_id, client_profile, audio_track?, subtitle_track?, start_sec?,
  max_bitrate_kbps?}`,
- **When** processed,
- **Then** the API:
  1. validates the user has access to the video,
  2. gRPC-calls Streaming's `OpenSession` with the request,
  3. mints a JWT signed URL `/stream/{session_id}/manifest.m3u8?sig=...`
     valid for `session_url_ttl_sec` (default 1800 s),
  4. inserts a `streaming_sessions` row,
  5. returns `{session_id, manifest_url, expires_at, ladder, current_rendition}`.

**AC-2 — Get session info.**
- **Given** an open session,
- **When** `GET /api/stream/sessions/{id}` is called,
- **Then** the response includes the bitrate ladder, current rendition,
  and `last_segment_fetched_at` for staleness diagnostics.

**AC-3 — Close session.**
- **Given** an open session,
- **When** `DELETE /api/stream/sessions/{id}` is called,
- **Then** the API gRPC-calls Streaming's `CloseSession`, the
  `streaming_sessions` row is updated `closed_at = now()`, and the
  response is `204`.

**AC-4 — Server capabilities.**
- **Given** any client,
- **When** `GET /api/stream/capabilities` is called,
- **Then** the response is `{codecs: [...], hwaccel: "videotoolbox"|...|
  "none", max_bitrate_kbps, supported_containers: [...]}`, fetched live
  from Streaming over gRPC and cached for 60 s in the API process.

**Test cases:**
- Unit: signed URL contains `aud=streaming`, `sub=session_id`, `exp =
  iat + ttl`, and `iss=api`.
- Integration: open + close round-trip writes both rows and frees the
  Streaming transcoder slot.
- Integration: open session with `start_sec=600` propagates to FFmpeg
  `-ss 600`.
- Integration: open session for a video the user can't access returns
  403 `type: access-denied` and Streaming is never called.
- Integration: capabilities endpoint hits the gRPC backend at most once
  per 60 s under load (cache TTL respected).

**Edge cases:**
- Streaming gRPC is down — `POST /api/stream/sessions` returns 503
  `type: streaming-unavailable` with `Retry-After: 5`. Test case: kill
  Streaming, hit the endpoint → 503.
- `start_sec` greater than `duration_sec` is clamped to `duration_sec - 5
  s` and a `Maktaba-Warning: start-sec-clamped` header is added.
- `client_profile` unknown — falls back to a generic profile that asks
  Streaming for HLS H.264 720p; logged at warn with the client UA.
- Two concurrent `POST /sessions` for the same `(user, video)` — both
  succeed (the user may legitimately watch the same video on two devices);
  rate-limited per-user via story 7.19.
- `manifest_url` expired before the player fetched it — Streaming returns
  401 with `type: signed-url-expired`; the client must call `POST
  /sessions` again. Document in the API reference.

---

### Story 7.11 — Watch progress sync

`POST /api/stream/sessions/{id}/progress` from §9.4 + WebSocket fan-out
on `/ws/playback/{video_id}` (story 7.16).

**AC-1 — Persist progress.**
- **Given** an open session,
- **When** `POST /api/stream/sessions/{id}/progress` is called with
  `{position_sec, completed?}`,
- **Then** `playback_state (user_id, video_id)` is upserted with the new
  position, `completed = true` is auto-set when `position_sec /
  duration_sec > 0.95`, and `updated_at = now()`.

**AC-2 — Fan out to other devices.**
- **Given** a user with two devices subscribed to
  `/ws/playback/{video_id}`,
- **When** progress is POSTed from device A,
- **Then** device B receives a frame `{type: "playback.progress",
  user_id, video_id, position_sec, completed, source_session_id}` within
  500 ms p95, including the `source_session_id` so device B can ignore
  echoes if it sent the original.

**AC-3 — Rate limit the firehose.**
- **Given** a player POSTing progress every 100 ms (misconfigured),
- **When** more than 1 POST per second is received per session,
- **Then** the additional POSTs are accepted with 200 OK but only the
  last per second is persisted (debounced server-side); WS fan-out
  matches the persistence cadence.

**Test cases:**
- Integration: position update arrives at the other device's WS within
  500 ms in a local docker compose env.
- Integration: `completed = true` triggers a separate `playback.completed`
  WS event in addition to the progress event, and updates `playback_state`
  in one transaction.
- Integration: a stale POST with `position_sec` lower than the current
  stored position is **still accepted** (user manually rewound) — no
  monotonicity check.

**Edge cases:**
- POST after `DELETE /sessions/{id}` — accepted with 200, persisted to
  `playback_state` (the watch happened, even if the session is closed).
- POST with `position_sec` greater than `duration_sec` is clamped, and a
  warning header is added.
- Network jitter causes POSTs to arrive out of order — the persistence
  uses `updated_at = now()` not the client clock, so the "latest received"
  wins, not the "latest in time."
- Disconnected client comes back online and bulk-replays 30 progress POSTs
  — the rate limiter (1/s) only persists ~30 entries spread over time;
  the final position is correct, intermediate ones may be coarsened.

---

### Story 7.12 — Job control endpoints

`POST /api/jobs/{id}/{pause,resume,cancel,retry}` and the per-video
shortcuts from §9.5 + §7.7. Idempotent flag-setters; never block on the
worker.

**AC-1 — Pause sets the flag, returns immediately.**
- **Given** a `running` job,
- **When** `POST /api/jobs/{id}/pause` is called,
- **Then** `pause_requested = true` is set in one UPDATE, the response is
  200 with the current job row (state still `running`), and a Postgres
  NOTIFY fires on `jobs.flag_set`. The actual state transition to
  `paused` happens asynchronously in the worker (§7.7) and is observed by
  the client over WS.

**AC-2 — Force pause.**
- **Given** a `running` job stuck inside a single segment for ≥
  `pause_grace_sec`,
- **When** `POST /api/jobs/{id}/pause?force=true` is called,
- **Then** the API directly UPDATEs `state='paused', paused_reason='user-force',
  paused_at_sec=last_segment_end_sec, claimed_by=NULL, pause_requested=false`,
  and the in-flight segment is discarded as documented in §7.7.

**AC-3 — Resume is a flag-clear.**
- **Given** a `paused` job,
- **When** `POST /api/jobs/{id}/resume` is called,
- **Then** the row's `paused_reason` is cleared (the job becomes
  re-claimable per §7.3), and the response is 200 with the unchanged
  state. The actual claim happens asynchronously.

**AC-4 — Cancel.**
- **Given** any non-terminal job,
- **When** `POST /api/jobs/{id}/cancel` is called,
- **Then** `cancel_requested = true` is set; the worker observes it after
  the next segment commit and transitions to `cancelled` (§7.7).

**AC-5 — Retry.**
- **Given** a `failed` job,
- **When** `POST /api/jobs/{id}/retry` is called,
- **Then** `attempts` is reset to 0, `state` flips to `pending`, `error`
  is cleared, and `not_before` is set to `now()` (cancels any backoff).
- **Given** a non-`failed` job, **Then** the response is `409 conflict`
  `type: job-not-failed`.

**AC-6 — Per-video aggregates.**
- **Given** a video with three active jobs across stages,
- **When** `POST /api/videos/{id}/pause` is called,
- **Then** every non-terminal job for that video has `pause_requested =
  true` set in one UPDATE, and the response includes `affected: 3`.

**AC-7 — Idempotency.**
- **Given** a job in any state,
- **When** the same control call is made twice,
- **Then** both responses are 200 with the same body (no error, no
  double-effect).

**Test cases:**
- Unit: state-machine guards reject illegal transitions (e.g. force-pause
  on a `done` job → 409 `type: job-terminal`).
- Integration: pause-then-resume cycle within 100 ms returns the job to
  `running` (worker re-claims it).
- Integration: NOTIFY on `jobs.flag_set` is observed by a listener test.
- Integration: per-video `pause` against a video with five jobs at mixed
  states only flips the non-terminal ones and reports the count.
- Integration: control endpoints take < 20 ms p99 under load (DB-only,
  no gRPC, no worker round-trip).

**Edge cases:**
- Pause on a `pending` job — sets `pause_requested = true`; the claim
  loop respects the flag and the job stays pending. Effectively a "freeze
  in queue" semantics. Document in the API reference.
- Resume on a `running` job — no-op; returns 200 with current state.
- Cancel on a `done` job — 409 `type: job-terminal`. Same for retry on
  `running`/`pending`.
- Force-pause race: the worker commits a segment while the API is
  setting `paused_at_sec=last_segment_end_sec` — the API uses a single
  UPDATE with the read inside (no read-then-write), so the value is
  always consistent.
- Mass per-video resume with 50 jobs — the UPDATE is one statement; the
  worker pool may not pick all up immediately (concurrency caps), so the
  response carries `affected` (DB rows updated), not `restarted`.

---

### Story 7.13 — Queue stats endpoint

`GET /api/queue/stats` from §9.5.

**AC-1 — Shape.**
- **Given** any queue state,
- **When** the endpoint is called,
- **Then** the response is `{by_stage: {scan: {pending, running, paused,
  failed, done_24h}, probe: {...}, ...}, eta_sec: N, total_in_flight: N,
  workers: [{id, host, last_heartbeat, current_job_id}]}`.

**Test cases:**
- Unit: `eta_sec` is a sum of `estimated_remaining_sec` over `running`
  jobs, divided by per-stage parallelism.
- Integration: the query is one SQL round trip with stage counts via
  `GROUP BY stage, state`.
- Performance: under 30 ms on a 100k-job table (only counts, no scans of
  big rows).

**Edge cases:**
- A stage has no jobs at all — included in the response with all zeros so
  the UI doesn't have to special-case the missing key.
- Workers heartbeat field is null for stale workers — surfaced in the
  response so the UI can highlight a "missing worker" warning.

---

### Story 7.14 — Collections, tags, speakers

Endpoints from §9.6.

**AC-1 — Collection CRUD with item ordering.**
- **Given** a collection and a list of `{video_id, position}` entries,
- **When** items are POSTed individually or replaced via PATCH on the
  collection,
- **Then** `position` is the canonical order; gaps are allowed; ties are
  broken by `(position, video_id)`. Re-ordering a single item is a single
  UPDATE.

**AC-2 — Smart collection.**
- **Given** a collection with `is_smart = true` and `smart_query` JSON,
- **When** the collection is read,
- **Then** items are computed live from `smart_query` (not stored in
  `collection_items`), with the same shape and pagination as a regular
  collection.

**AC-3 — Tag delta semantics.**
- **Given** a video with tags `[a, b, c]`,
- **When** `PATCH /api/videos/{id}/tags` is called with `{add: [d],
  remove: [b]}`,
- **Then** the resulting tags are `[a, c, d]`. Both `add` and `remove`
  are optional; absent means "no change."

**AC-4 — Speaker rename and merge.**
- **Given** two speakers,
- **When** `POST /api/speakers/merge {keep, drop}` is called,
- **Then** `segment_speakers.speaker_id = drop` rows are rewritten to
  `keep` in one transaction, the `drop` row is deleted, and the response
  is 200 with `affected_segments`.

**Test cases:**
- Integration: item ordering survives a re-order roundtrip.
- Integration: smart collection respects pagination cursor for the
  underlying live query.
- Integration: tag dedup — adding an existing tag is a no-op, not 409.
- Integration: speaker merge is one transaction; killing the API
  mid-merge leaves no half-state.

**Edge cases:**
- A `smart_query` with a deeply nested filter that is invalid at read
  time — return 200 with `items: []` and a `warning` field; never 500.
- Rename a speaker to a name another speaker already has — 409 `type:
  speaker-name-exists`; suggest merge instead.
- Tag name normalization: trim whitespace, NFC unicode normalize, fold
  case-insensitively for uniqueness checks but preserve original
  casing for display. Test case: tags `"Tafsir"` and `"tafsir"` produce
  one row.

---

### Story 7.15 — Settings & system endpoints

Endpoints from §9.7. Reads everything readable; writes only what is
UI-editable per §11.1 (runtime knobs only).

**AC-1 — Read settings (with redaction).**
- **Given** a config containing `api_key` fields,
- **When** `GET /api/settings` is called,
- **Then** the response is the merged effective config (file + env + DB)
  with every secret-bearing key replaced by `"<redacted>"` and a sibling
  `*_present: true`.

**AC-2 — Patch settings (DB-backed only).**
- **Given** a request to PATCH a runtime knob (e.g. `search.fts_weight`),
- **When** the value is in range,
- **Then** the change is persisted to `app_settings`, takes effect within
  one settings reload (5 s polling or `LISTEN settings_changed`), and the
  response is the merged effective config.

**AC-3 — Patch denied for non-runtime keys.**
- **Given** a request to PATCH `database.url`,
- **When** sent,
- **Then** the response is `403 Forbidden` `type: setting-not-runtime`.

**AC-4 — STT backends listing.**
- **Given** any deployment,
- **When** `GET /api/settings/stt-backends` is called,
- **Then** the response enumerates `{name, available, version,
  models, hwaccel, cost_per_minute_usd?}` for each backend, sourced from
  Pipeline gRPC `ListBackends` and cached 60 s.

**AC-5 — STT dry-run.**
- **Given** a backend + config,
- **When** `POST /api/settings/stt-test` is called,
- **Then** Pipeline runs a 10 s synthetic-speech transcription and
  returns `{ok, latency_ms, sample_text, error?}`.

**Test cases:**
- Integration: a PATCH that fails validation returns 422 with the
  invalid field listed.
- Integration: the settings change Postgres NOTIFY is received by a
  second API replica within 1 s.
- Security: never returns a value that looks like a secret (regex on
  `key|token|password|secret` in the response body verifies redaction).

**Edge cases:**
- Settings drift between two API replicas during a partial NOTIFY loss —
  the 5 s poll backstop reconciles within at most 5 s. Test case:
  simulate dropped NOTIFY → state converges by next poll.
- A patch to a value that bricks search (e.g. `fts_weight = -1`) is
  rejected by the validator with 422; the config never reaches the
  runtime in a broken state.

---

### Story 7.16 — WebSocket fan-out

`/ws/jobs`, `/ws/library/{id}`, `/ws/playback/{video_id}` per §6.2 +
§7.10. SSE fallback for blocked-WebSocket networks (§6.2).

**AC-1 — WebSocket auth at handshake.**
- **Given** a connect request,
- **When** the upgrade is processed,
- **Then** the connection is accepted only if the request carries a valid
  JWT (Authorization header for native clients; cookie for web clients).
  Failure → close with code 4401.

**AC-2 — Subscription scoping.**
- **Given** a connected client,
- **When** events flow through Postgres LISTEN,
- **Then** the client only receives events they are authorized to see
  (per-user `playback`; per-library `library`; jobs are admin-only by
  default, configurable via per-user permission).

**AC-3 — Event shape.**
- **Given** any event,
- **When** sent over the wire,
- **Then** the JSON envelope is `{type: "<channel.event>", at: "<RFC3339>",
  ...payload}` and the type names are stable (semver — additions allowed,
  renames forbidden).

**AC-4 — Backpressure.**
- **Given** a slow client whose receive buffer fills up,
- **When** the server's per-connection send queue exceeds 1000 frames or
  1 MiB,
- **Then** the connection is closed with code 1011 `slow-consumer` and
  the listener row is freed. The client is expected to reconnect with a
  cursor (`?since=<at>`) and replay from a server-side ring buffer (last
  60 s).

**AC-5 — Heartbeat / idle close.**
- **Given** an idle WebSocket,
- **When** no frame is sent or received for 30 s,
- **Then** the server sends a ping; if no pong arrives within 10 s the
  connection is closed.

**AC-6 — SSE fallback.**
- **Given** a client that requests `/ws/jobs` with `Accept:
  text/event-stream` instead of WebSocket upgrade,
- **When** processed,
- **Then** the same event stream is delivered as SSE frames with the same
  envelope.

**Test cases:**
- Integration: connect without auth → 401 close; connect with valid auth
  → ping/pong cycle proven over 60 s.
- Integration: a job state change in DB → subscribed client receives a
  `job.state_changed` event within 200 ms.
- Integration: 1000 simultaneous connections in a load test → memory
  stays under 200 MB (per architecture choice of `coder/websocket`).
- Integration: slow consumer test — drop receive on the client → server
  closes with 1011 inside 5 s.
- Integration: SSE fallback delivers the same first 10 events as the WS
  variant for the same fixture.

**Edge cases:**
- Postgres connection drops while the listener is active — the listener
  reconnects with exponential backoff; events lost during the gap are
  not replayed (clients reconcile via REST `GET /api/jobs`). Surface
  the gap to clients via a `gap` event with `from`/`to` timestamps.
- Two API replicas both LISTEN on the same channel — both receive the
  NOTIFY and both deliver to their own connected clients (fanout is
  per-replica). Document in operations.
- A client subscribes to `/ws/library/{id}` for a library they no longer
  have access to (revoked mid-session) — the next event is intercepted
  and the connection is closed with 4403 `forbidden`.
- WebSocket upgrade behind a buggy proxy that strips `Connection: Upgrade`
  — the SSE fallback is the documented escape hatch.

---

### Story 7.17 — GraphQL schema + resolvers

Schema-first via `gqlgen`; resolvers wrap the same domain code as REST
(§9 intro). Subscriptions are WebSocket-based and reuse the §7.16
fan-out.

**AC-1 — Domain types in the schema.**
- **Given** the `shared/graphql/schema.graphql`,
- **When** parsed,
- **Then** it contains `Library`, `Video`, `MediaInfo`, `AudioTrack`,
  `Transcript`, `Segment`, `Word`, `Subtitle`, `Chapter`, `Tag`,
  `Collection`, `Speaker`, `Job`, `StreamingSession`, `User`,
  `PlaybackState`, `SearchResult`, `SearchHit`, `SearchMatch` and a
  matching set of input types for mutations.

**AC-2 — Query + Mutation parity with REST.**
- **Given** every REST endpoint listed in §9.1–9.7,
- **When** the schema is read,
- **Then** there is a corresponding `Query` field (for reads) or
  `Mutation` field (for writes) implemented by the same domain function
  the REST handler calls.

**AC-3 — Subscription parity with WebSocket.**
- **Given** every channel in story 7.16,
- **When** the schema is read,
- **Then** there are `Subscription.jobUpdates`, `Subscription.libraryEvents
  (libraryId)`, and `Subscription.playbackUpdates(videoId)` resolvers
  that filter the same NOTIFY stream.

**AC-4 — DataLoader against N+1.**
- **Given** a query asking for 100 videos with `media_info` and `audio_tracks`,
- **When** the resolver runs,
- **Then** at most 3 SQL queries are issued (videos, media_info bulk,
  audio_tracks bulk), proven by a query-counting test.

**AC-5 — Persisted queries.**
- **Given** a client that POSTs `{persistedQueryId}` instead of `{query}`,
- **When** the API has the query in its persisted-store,
- **Then** the server resolves it; otherwise returns
  `PersistedQueryNotFound` with the standard Apollo shape and the client
  retries with the full query.

**Test cases:**
- Schema test: `gqlgen` generation runs in CI; a missing resolver fails
  the build.
- Integration: a query returning 1000 videos uses ≤4 SQL round trips.
- Integration: subscription receives the same job-progress events as
  the REST WS endpoint over the same fixture.
- Integration: a malformed query returns a GraphQL error envelope (not
  problem+json) with `extensions.code` set.

**Edge cases:**
- A field selection that asks for `playbackState` on a video the user
  hasn't watched — return `playbackState: null`, not an error.
- Subscriptions over the same WS connection share one Postgres listener
  per channel (multiplexed) — confirmed by a load test that opens 100
  subscriptions and asserts only one `LISTEN jobs.*` per replica.
- Mutation that fails partway (e.g. tag patch with one valid + one
  invalid tag) — entire mutation is one transaction; on failure no tags
  are changed and the response carries the per-tag error array.
- Field-level cost limit — a query that requests `transcripts.segments`
  with `first: 100000` is rejected with `cost-limit-exceeded`.

---

### Story 7.18 — gRPC clients to Pipeline and Streaming

The API consumes the gRPC schemas from `shared/proto/` (§9.9). One
client wrapper per service, with timeouts, retries, and circuit breaking.

**AC-1 — Pipeline client interface.**
- **Given** the generated `pipeline.PipelineClient`,
- **When** wrapped,
- **Then** the API exposes
  `pipeline.Embed(ctx, text) (Vector, error)`,
  `pipeline.Transcribe(ctx, req) (<-chan TranscribeEvent, error)`,
  `pipeline.ListBackends(ctx) ([]Backend, error)`,
  `pipeline.HealthCheck(ctx) (Status, error)`,
  with per-call deadlines from config.

**AC-2 — Streaming client interface.**
- **Given** the generated `streaming.StreamingClient`,
- **When** wrapped,
- **Then** the API exposes `streaming.OpenSession`,
  `streaming.CloseSession`, `streaming.EvictHashCache`,
  `streaming.HealthCheck`.

**AC-3 — Retry and circuit breaker.**
- **Given** a transient gRPC failure (`UNAVAILABLE`, `DEADLINE_EXCEEDED`),
- **When** the client retries,
- **Then** retries are bounded (default 3, jittered exponential backoff),
  and after `failure_rate > 50% in 30 s` the breaker opens for 10 s and
  fails fast with a `circuit-open` error.

**AC-4 — Context propagation.**
- **Given** an incoming HTTP request with `X-Request-Id`,
- **When** the gRPC call is made,
- **Then** the request ID is carried over via gRPC metadata
  (`maktaba-request-id`) and appears in the receiving service's logs.

**Test cases:**
- Integration: retry path proven by a fake gRPC server that fails twice
  then succeeds.
- Integration: circuit opens after 10 consecutive failures, closes after
  a successful probe call.
- Integration: deadline propagation — a 100 ms HTTP timeout caps the
  gRPC call to ≤100 ms.
- Integration: tracing — when OTel is enabled, the gRPC call inherits the
  HTTP span as parent.

**Edge cases:**
- Pipeline returns an `INTERNAL` error — surfaced to the caller as a
  500 problem+json `type: pipeline-internal`, never a 200 with empty
  result.
- Streaming returns `RESOURCE_EXHAUSTED` (transcoder slots full) — the
  API translates to 503 problem+json with `Retry-After: 5`. Not retried
  inside the client (the user must back off).
- The protobuf schema adds a new optional field — old clients ignore it
  silently; tests assert this with a forward-compat fixture.

---

### Story 7.19 — Validation, body limits, rate limiting

Cross-cutting middleware that every story above relies on.

**AC-1 — Body size cap.**
- **Given** a request with a JSON body larger than 1 MiB (default),
- **When** received,
- **Then** the response is `413 Payload Too Large` problem+json before
  the handler executes.

**AC-2 — Content-Type enforcement.**
- **Given** a non-GET request without `Content-Type: application/json` (or
  `application/graphql+json` for the GraphQL endpoint),
- **When** received,
- **Then** the response is `415 Unsupported Media Type` problem+json.

**AC-3 — Struct-tag validation.**
- **Given** a handler whose request struct has `validate:"required,uuid"`,
- **When** the body is `{id: "not-a-uuid"}`,
- **Then** the response is `422 Unprocessable Entity` problem+json with
  `errors: [{field: "id", message: "must be a valid UUID"}]`.

**AC-4 — Per-user rate limit.**
- **Given** a user identity (cookie or JWT),
- **When** they exceed `default_rate_per_min` (default 600) on the API
  surface,
- **Then** further requests return `429 Too Many Requests` with
  `Retry-After: <sec>` and a problem+json body.
- **Given** the unauthenticated `/api/auth/*` surface, lower defaults
  apply (`auth_rate_per_min`, default 30). Coordinated with Epic 10.

**AC-5 — Per-IP rate limit (DoS guard).**
- **Given** any single IP,
- **When** total request rate exceeds `ip_rate_per_min` (default 6000),
- **Then** further requests from that IP return 429 regardless of
  authentication.

**Test cases:**
- Unit: 1 MiB +1 byte body → 413 without invoking the handler.
- Integration: malformed JSON → 400 `type: invalid-json`, not 500.
- Integration: 700 valid requests in 60 s as one user → ~600 200s and
  ~100 429s.
- Integration: `Retry-After` header value is consistent with the
  rate-limit window.
- Security: a `Content-Length: 1000000000` header with a small body does
  not trick the limiter (use `LimitReader`).

**Edge cases:**
- A user behind a corporate NAT shares an IP with 100 colleagues — the
  per-IP limit is generous enough (6000/min) that legitimate usage isn't
  cut off; per-user rate limit is the dominant constraint.
- A streaming-progress POST burst (story 7.11) is excluded from the
  general API rate limit and uses its own 1/s/session debounce.
- Body limit configurable per-route — `/api/videos/{id}` PATCH allows 8
  KB, `/api/search` POST allows 16 KB, default 1 MiB. Test case: route
  with 8 KB cap rejects 16 KB body even though the global cap is 1 MiB.

---

### Story 7.20 — Health, version, metrics

`/api/system/health`, `/api/system/version` (§9.7), plus optional
OpenTelemetry export (§2.1).

**AC-1 — Health composition.**
- **Given** the API needs DB, Pipeline gRPC, and Streaming gRPC,
- **When** `GET /api/system/health` is called,
- **Then** the response is `{status: "ok"|"degraded"|"down",
  components: {db: ..., pipeline: ..., streaming: ...}, checked_at}` and
  the HTTP status reflects the worst component (`200` ok/degraded, `503`
  down).

**AC-2 — Version endpoint.**
- **Given** the binary is built with `-ldflags "-X main.version=..."`,
- **When** `GET /api/system/version` is called,
- **Then** the response is `{version, build_sha, build_time, go_version,
  schema_revision}`.

**AC-3 — Metrics export.**
- **Given** `[telemetry].enabled = true`,
- **When** the API runs,
- **Then** `/metrics` (separate port `metrics_listen`) exposes Prometheus
  metrics including `http_requests_total`, `http_request_duration_seconds`,
  `grpc_client_calls_total`, `ws_active_connections`,
  `db_pool_in_use`, `db_pool_idle`, `job_queue_pending` per stage.

**AC-4 — OTel traces opt-in.**
- **Given** `[telemetry].otel_endpoint` is set,
- **When** any HTTP or gRPC call is made,
- **Then** spans are emitted with consistent service name `maktaba-api`,
  attributes include `http.route`, `http.status_code`, `db.statement`
  (truncated), and parent context is propagated via W3C `traceparent`.

**Test cases:**
- Integration: kill Pipeline → `/health` reports `pipeline: "down"` and
  the overall status `degraded` (Streaming and DB still up).
- Integration: kill Postgres → `/health` returns 503 `down`.
- Integration: `/metrics` is not authenticated by default (assumed
  bound to localhost), but is configurable to require an admin token.
- Integration: a single request appears as one span tree spanning API +
  Pipeline.

**Edge cases:**
- Health-check storm from a misconfigured Kubernetes liveness probe —
  the health endpoint is cached 1 s to avoid hammering Pipeline gRPC. Test
  case: 100 health calls in 1 s → only 1 gRPC call to Pipeline.
- Schema revision mismatch (binary expects v15, DB is at v14) — `/health`
  reports `db: degraded` with `detail: "schema-behind"`; the binary still
  serves read-only requests but blocks writes that would need v15. Test
  case: skip a migration → /health is degraded; PATCH fails with 503
  `type: schema-out-of-date`.

---

## Epic 8 — Streaming Service

The Go Streaming Service is every media byte: HLS and DASH manifests,
range-served direct play, FFmpeg-driven on-the-fly transcode and remux,
live subtitle muxing, sprite/poster serving, and session-pinned adaptive
playback (§4). It is its own binary, shares only Postgres and the
read-only media volume with the rest of the system, and validates its
own JWTs offline against the API's published JWKS so it can keep an
in-flight watch session alive even when the API restarts (§9.4).

This epic covers the byte-pumping HTTP surface (§9.4 "Streaming Service"
section), the gRPC server consumed by the API (§9.9), and the FFmpeg
orchestration that backs each playback mode (§4.1–4.9). Session
*creation* is owned by the API (Story 7.10); this epic implements the
gRPC handler that accepts the create call and the byte handlers that
serve the resulting URLs.

**Out of scope for Epic 8:** subtitle *generation* (Pipeline / Epic 1 in
the other doc), thumbnail *generation* (Pipeline), session *minting*
(Story 7.10), JWT *issuance* (Epic 10). The Streaming Service consumes
all of those.

### Story map

| #     | Story                                                | Depends on |
|-------|------------------------------------------------------|------------|
| 8.1   | Server skeleton, signed URL middleware, metrics     | —          |
| 8.2   | Capability matrix and client profile registry       | 8.1        |
| 8.3   | Direct play (range-served `206 Partial Content`)    | 8.1, 8.2   |
| 8.4   | Direct stream (FFmpeg `-c copy` remux)              | 8.1, 8.2   |
| 8.5   | HLS adaptive transcode pipeline                      | 8.1, 8.2   |
| 8.6   | DASH manifest (opt-in per session)                   | 8.5        |
| 8.7   | Hardware acceleration auto-detect                    | 8.5        |
| 8.8   | gRPC server: OpenSession / CloseSession / EvictHashCache | 8.1, 8.5 |
| 8.9   | Session store, sticky transcoder, reaper             | 8.8        |
| 8.10  | Concurrency caps, backpressure, slot accounting      | 8.5, 8.9   |
| 8.11  | Live subtitle rendering (auto, sidecar, embedded)    | 8.5, 8.1   |
| 8.12  | Chapter delivery (HLS DATERANGE + sidecar JSON)      | 8.5, 8.1   |
| 8.13  | Posters, sprite sheets, chapter thumbs serving       | 8.1        |
| 8.14  | Cache layout, LRU GC, cap enforcement                | 8.1        |
| 8.15  | Probe cache (LRU + Postgres)                         | 8.1        |

---

### Story 8.1 — Server skeleton, signed URL middleware

The bytes-only HTTP surface. Every Streaming endpoint runs through one
middleware that validates a signed JWT against the API's public JWKS,
extracts the session id, and rejects expired or wrong-audience tokens.

**AC-1 — Signed URL validation.**
- **Given** a request `GET /stream/{session_id}/{path}?sig={jwt}`,
- **When** the middleware runs,
- **Then** the JWT is verified RS256 against the public key cached from
  `GET <api_origin>/.well-known/jwks.json`, the `aud` claim must equal
  `streaming`, the `sub` claim must equal `session_id`, and `exp` must
  be in the future. Failure → `401 Unauthorized` problem+json with
  `type: signed-url-invalid` (the four sub-types `missing`, `expired`,
  `wrong-aud`, `wrong-sub`, `bad-signature` carried in `detail`).

**AC-2 — JWKS refresh.**
- **Given** the cached JWKS is older than `jwks_refresh_sec` (default
  300 s),
- **When** the next request arrives,
- **Then** the JWKS is refreshed asynchronously (the in-flight request
  uses the cached key); on refresh failure the cache is kept (don't
  invalidate working keys on transient API outage).

**AC-3 — Range-correct error envelopes.**
- **Given** an authenticated request to a missing segment,
- **When** processed,
- **Then** the response is `404 Not Found` problem+json (NOT 200 with
  empty body — players treat empty 200 as "stream ended").

**AC-4 — Direct-play JWT in query string.**
- **Given** `GET /stream/direct/{video_id}?sig=<jwt>`,
- **When** processed,
- **Then** the JWT is validated as in AC-1 except `aud=streaming-direct`
  and `sub=video_id`. The endpoint also accepts `Authorization: Bearer
  <jwt>` for native players that prefer headers.

**Test cases:**
- Unit: an unsigned URL → 401 `type: signed-url-missing`.
- Unit: JWT signed with the wrong key → 401 `type: bad-signature`.
- Unit: JWT for a different session id → 401 `type: wrong-sub`.
- Integration: JWKS endpoint returning a new key id → next request
  succeeds without restart.
- Integration: 1000 parallel requests to the same session URL → the JWKS
  is fetched at most once.
- Security: an attacker who steals a manifest URL can only access that
  session's segments (sub claim) for the remaining TTL.

**Edge cases:**
- Clock skew between API and Streaming up to ±60 s — `exp` is checked
  with a `clock_skew_leeway_sec` (default 60). Test case: a JWT with
  `exp = now()-30s` is still accepted.
- API is down so JWKS can't refresh — keep using the cached key
  indefinitely; emit a warning metric `jwks_refresh_failed_total`.
  Test case: kill API, requests using existing session continue to work.
- The API rotates its signing key — the new key id is in the JWKS;
  Streaming picks it up on next refresh; old in-flight URLs (signed by
  the old key) keep working until they expire because the JWKS contains
  both keys during rotation. Documented in Epic 10.
- A player that retries a failed segment with the same URL after JWT
  expiry — receives 401 once, then must call `POST /api/stream/sessions`
  to mint a fresh URL. The Streaming binary never extends a JWT.

---

### Story 8.2 — Capability matrix & client profile registry

Each session is opened with a `client_profile` (browser-chrome, browser-safari,
ios-native, android-native, tvos, androidtv, generic). The matrix maps
profile → supported `(container, video_codec, audio_codec, profile_level,
hdr_format)` tuples. Used by stories 8.3/8.4/8.5 to decide direct/remux/transcode.

**AC-1 — Matrix lookup.**
- **Given** a known profile,
- **When** asked `canDirectPlay(profile, mediaInfo)`,
- **Then** returns true iff every (container, video, audio) tuple of the
  source is in the profile's allow-list at a profile/level the client can
  decode.

**AC-2 — Per-session overrides.**
- **Given** a session opened with `force_transcode=true` or
  `max_bitrate_kbps=1500`,
- **When** the matrix is consulted,
- **Then** the override beats the profile default — direct/remux is
  skipped or the bitrate ceiling is enforced in the ladder.

**AC-3 — Unknown profile fallback.**
- **Given** a profile name not in the registry,
- **When** queried,
- **Then** the `generic` profile is used (HLS H.264 + AAC, max 720p) and
  a warning is logged with the supplied profile name and request UA.

**Test cases:**
- Unit table-driven: each profile × representative MKV/MP4/WebM source →
  expected mode (direct, remux, transcode).
- Unit: HEVC source on `browser-chrome` → transcode; same source on
  `browser-safari` (post-2020) → direct.
- Unit: AC-3 audio on `ios-native` → remux to AAC needed even if video
  is fine.

**Edge cases:**
- A profile that lies (claims H.265 but actually fails to decode at
  runtime) — out of scope; the user can flip the override per-session.
- Profile registry update without restart — the registry is reloaded on
  `LISTEN profiles_changed` (matches Epic 7's settings reload pattern).

---

### Story 8.3 — Direct play (range-served `206 Partial Content`)

The fast path: zero transcoding, zero remuxing. `GET /stream/direct/{video_id}`
serves the underlying file with full HTTP range support so any player
(browser, AVPlayer, ExoPlayer, VLC) can seek without server CPU.

**AC-1 — Conditional GET + range serving.**
- **Given** an authenticated request for a video that the matrix marks
  direct-playable for the requesting profile,
- **When** the request specifies `Range: bytes=N-M`,
- **Then** the response is `206 Partial Content` with `Content-Range:
  bytes N-M/total`, `Accept-Ranges: bytes`, `Content-Length: M-N+1`,
  correct `Content-Type` from probe metadata, and the bytes match the
  file slice.

**AC-2 — HEAD support.**
- **Given** a HEAD request,
- **When** processed,
- **Then** the response is `200 OK` with the same headers minus body,
  including correct `Content-Length`. Required by Safari before it
  attempts ranged GET.

**AC-3 — Multi-range refusal.**
- **Given** `Range: bytes=0-100,200-300` (multipart range),
- **When** received,
- **Then** the response is `416 Range Not Satisfiable` (we don't ship
  multipart/byteranges). Players degrade to single-range automatically.

**AC-4 — Falls through to remux/transcode if not direct-playable.**
- **Given** a video that the matrix marks not-direct-playable,
- **When** `GET /stream/direct/{video_id}` is called,
- **Then** the response is `409 Conflict` `type: direct-play-unsupported`
  with a `manifest_url` in `detail` pointing the client at the session
  manifest. Native clients should never reach this path; web players
  always call `POST /api/stream/sessions` first.

**Test cases:**
- Unit: range parsing — `bytes=-100` (suffix), `bytes=100-` (open-end),
  `bytes=100-200` (closed) all produce correct `Content-Range`.
- Integration: Safari range probe (HEAD then GET `bytes=0-1`) → both
  succeed.
- Integration: streaming a 4 GB MP4 in parallel from two clients →
  bandwidth scales linearly; CPU stays under 5% of one core.
- Integration: a request for a `.mkv` from `browser-chrome` returns 409
  with the manifest URL.
- Performance: p99 latency for first byte under 50 ms on local SSD.

**Edge cases:**
- File modified during read (mtime changed) — the response includes
  `Last-Modified` and `ETag` (BLAKE3 prefix); a stale request with `If-
  Range` against a changed ETag is served with `200 OK` full body so the
  player resyncs cleanly.
- File on a network filesystem disappears mid-stream — `io.Copy` errors;
  the connection is closed, the error is logged, no panic. Test case:
  unmount during stream → graceful close.
- Range past EOF — `416 Range Not Satisfiable` with `Content-Range:
  bytes */total`.
- A client sending `Range: bytes=0-9999999999` for a 1 GB file — the
  range is clamped to file size in the response, not rejected.

---

### Story 8.4 — Direct stream (remux only)

Same codecs, wrong container. FFmpeg `-c copy` rewraps; near-zero CPU,
zero quality loss. Result is cached LRU at `cache/remux/{hash}/`.

**AC-1 — Cache-then-stream.**
- **Given** a video whose matrix verdict is "remux to MP4 fragmented",
- **When** the manifest is fetched for the first time,
- **Then** FFmpeg writes `cache/remux/{hash[:2]}/{hash}.mp4` via a
  temp file + atomic rename, and the manifest serves it via the direct
  play range-server (story 8.3) once written. Response while writing is
  HTTP 503 with `Retry-After: 2`.

**AC-2 — Cache hit serves immediately.**
- **Given** the remuxed file already exists,
- **When** requested,
- **Then** no FFmpeg is spawned and the file is range-served directly.

**AC-3 — Streaming write (preferred).**
- **Given** the remux is small enough to start serving partial bytes
  before completion,
- **When** the request is the very first one for this video,
- **Then** the response begins streaming as FFmpeg writes (chunked
  transfer-encoding, `Content-Length` omitted), and subsequent requests
  hit the cached file. Implementation may opt to skip this and always do
  AC-1's cache-then-stream behavior; the AC is "either is acceptable as
  long as the user-perceived TTFB is < 500 ms on local disk."

**Test cases:**
- Integration: MKV (H.264 + AAC) for `ios-native` → `cache/remux/.../*.mp4`
  exists after first request and serves with no FFmpeg on second.
- Integration: corrupt cache file detected by ffprobe → invalidated and
  regenerated.
- Integration: simultaneous first-request from two clients → only one
  FFmpeg subprocess (single-flight by `content_hash`).
- Integration: `LRU` eviction reclaims the file when the remux cache
  exceeds its share of the cap.

**Edge cases:**
- Source file changes (mtime) while remux exists — the cache key
  includes the file's `content_hash`, so any bit-level change yields a
  new cache entry. Stale entries are garbage collected by story 8.14.
- Remux fails partway (corrupt source) — temp file is removed; error is
  surfaced to the client as `502 Bad Gateway` `type: remux-failed`; the
  matrix verdict for this video is downgraded to transcode for the
  remainder of the session.

---

### Story 8.5 — HLS adaptive transcode pipeline

The fallback for everything direct/remux can't handle. One FFmpeg
subprocess per session writes a ladder of H.264+AAC renditions; segments
are served out of `cache/hls/{session_id}/` with a rolling window
(`hls_list_size=6` by default). FFmpeg flags per §4.4.

**AC-1 — Manifest assembly.**
- **Given** an open session with default ladder `[1080p, 720p, 480p]`,
- **When** `GET /stream/{session_id}/manifest.m3u8` is fetched,
- **Then** the response is the master playlist exactly matching §4.3
  shape (variant streams, audio group, subtitle group), with
  `Cache-Control: no-store` (manifests are dynamic).

**AC-2 — Variant playlists update live.**
- **Given** the FFmpeg subprocess writing segments,
- **When** `GET /stream/{session_id}/{rendition}/index.m3u8` is fetched
  every ~2 s by the player,
- **Then** the playlist contains the latest 6 segments
  (`#EXT-X-MEDIA-SEQUENCE` advances), already-deleted segments are not
  listed, and `#EXT-X-ENDLIST` is present once FFmpeg exits cleanly.

**AC-3 — Segment serving.**
- **Given** an authenticated segment request,
- **When** the segment file exists on disk,
- **Then** it's served with `Content-Type: video/MP2T`, `Cache-Control:
  public, max-age=31536000, immutable` (segments are content-addressed by
  session id + sequence), and the JWT signature checked.
- **When** the segment file does not yet exist (player is asking too far
  ahead),
- **Then** the request waits up to `segment_wait_ms` (default 5000) for
  the file to appear, polling at 100 ms; if it never appears returns
  `404`.

**AC-4 — Bitrate ceiling.**
- **Given** a session opened with `max_bitrate_kbps=1500`,
- **When** the master playlist is built,
- **Then** the ladder excludes any rendition whose `BANDWIDTH > 1500000`.

**AC-5 — Seek triggers cold restart.**
- **Given** a player issues a seek beyond the rolling window,
- **When** the new range is requested,
- **Then** the session-pinned FFmpeg is killed and respawned with a new
  `-ss {start_sec}`; the master playlist's discontinuity tag is emitted;
  the player resumes within 2 s p95.

**Test cases:**
- Integration: a 30 s sample MKV transcoded → master + 3 variant
  playlists + at least 8 segments; each segment plays via ffprobe.
- Integration: HLS validator (e.g., `mediastreamvalidator` from Apple
  HLS tools) passes against the produced playlists (CI gate).
- Integration: rolling window — the 7th segment causes the 1st to be
  removed from the playlist and the file deleted from disk.
- Integration: bitrate cap of 1500 → 480p is the only rendition served.
- Integration: seek to t=600 in a 1200 s video — new FFmpeg starts at
  600, segment-zero is served within 2 s.

**Edge cases:**
- Player requests segment 0 after FFmpeg has rolled past it — return
  410 Gone (not 404); the player should reload the playlist.
- FFmpeg process crashes mid-stream — the session reaper (story 8.9)
  catches it; the player gets 502s; on player retry the session is
  marked failed, the API is notified, and the user sees a "playback
  error" toast.
- Player asks for a segment for a closed session — 404 immediately,
  don't wait `segment_wait_ms`.
- A network filesystem latency spike causes FFmpeg to fall behind the
  player's playback — the player buffers underrun; we surface this as a
  metric `hls_segment_starvation_total` and the player downshifts.
- Independent segment alignment for ABR switching: keyframe interval is
  forced to 2 s (`-g 48 -keyint_min 48` at 24 fps) regardless of source,
  so renditions can interleave at any segment boundary.

---

### Story 8.6 — DASH manifest (opt-in per session)

DASH is opt-in because §4.3 notes the FFmpeg single-encode → both-formats
trick isn't supported; running both formats for one session would double
CPU. Players that need DASH (some Android browsers) request it
explicitly via `format=dash` on session open.

**AC-1 — DASH-only session.**
- **Given** a session opened with `format=dash`,
- **When** `GET /stream/{session_id}/manifest.mpd` is fetched,
- **Then** an MPD with the same ladder as the HLS variant is returned,
  segments are `init.mp4` + `chunk-N.m4s`, and the player can play it.
- **When** the same session is asked for HLS, it returns 409
  `type: format-mismatch`.

**AC-2 — Validation.**
- **Given** any produced MPD,
- **When** validated against the DASH-IF MPD validator,
- **Then** it passes baseline conformance.

**Test cases:**
- Integration: shaka-player can play a fixture session.
- Integration: format=dash + format=hls cannot coexist on a session id.

**Edge cases:**
- Subtitle handling is identical (VTT) but referenced differently in the
  MPD. Documented in story 8.11.
- DASH live profile vs static — sessions are live (`type="dynamic"`)
  during playback, switched to `static` on EOF.

---

### Story 8.7 — Hardware acceleration auto-detect

Per §4.4: VideoToolbox on Apple Silicon, NVENC on NVIDIA, QuickSync on
Intel, libx264 fallback. Detected once at startup; per-session overridable.

**AC-1 — Boot-time detection.**
- **Given** the binary starts on macOS,
- **When** `ffmpeg -encoders` is parsed,
- **Then** `h264_videotoolbox` is selected; logged at info; exposed via
  `streaming.HealthCheck` and `GET /api/stream/capabilities`.
- **Given** the binary starts on Linux with an NVIDIA GPU and
  `nvidia-smi` succeeds,
- **Then** `h264_nvenc` is selected.
- **Given** none of the above,
- **Then** `libx264 -preset veryfast` is selected.

**AC-2 — Per-session override.**
- **Given** `force_software=true` on session open,
- **When** FFmpeg is spawned,
- **Then** software libx264 is used regardless of detected hwaccel
  (useful for problematic source files that crash the hardware path).

**AC-3 — Hwaccel failure fallback.**
- **Given** a session where the hwaccel encoder errors out within the
  first segment,
- **When** the failure is detected (FFmpeg exits non-zero before segment
  1),
- **Then** the session is restarted once with software encoding; if that
  also fails the session is closed with `502 Bad Gateway` and the matrix
  verdict for the source is recorded as transcode-failed.

**Test cases:**
- Unit: encoder selection table for {macOS-arm64, macOS-x86, Linux+nvidia,
  Linux+intel, Linux+none, Windows+intel}.
- Integration: spawn fixture session with `force_software=true` → no
  `videotoolbox` arguments in the FFmpeg command line.
- Integration: simulate hwaccel failure (mock FFmpeg) → fallback path
  succeeds.

**Edge cases:**
- Hardware decoder limit reached (e.g. NVENC concurrent session cap on
  consumer GPUs is 3) — new sessions over the cap fall back to software
  even though detection said NVENC. Tracked via metric
  `hwaccel_session_capacity_exceeded_total`.
- Source file uses a feature the hardware encoder doesn't support (e.g.
  HEVC 10-bit input on a NVENC SKU that lacks it) — caught by the AC-3
  fallback.

---

### Story 8.8 — gRPC server (Open/Close/EvictHashCache)

The Streaming binary exposes the gRPC schema from §9.9. The API is the
sole gRPC client. All endpoints are non-streaming except future
HealthCheck-watch (out of scope here).

**AC-1 — OpenSession.**
- **Given** a request `{video_id, client_profile, audio_track?,
  subtitle_track?, start_sec?, max_bitrate_kbps?, format?}`,
- **When** processed,
- **Then** the Streaming server:
  1. fetches the probe metadata (story 8.15) for `video_id`,
  2. consults the matrix (story 8.2) and chooses direct/remux/transcode,
  3. allocates a session id, inserts a `streaming_sessions` row in the
     same transaction the API is using (passed via gRPC metadata?
     **no** — Streaming opens its own transaction; the API records the
     session id it gets back),
  4. for transcode/remux modes, spawns the FFmpeg subprocess and waits
     for the master playlist file to appear (capped at 5 s),
  5. returns `{session_id, mode, ladder?, manifest_path, expires_at}`
     where `manifest_path` is the relative path the API will sign.

**AC-2 — CloseSession.**
- **Given** a session id,
- **When** `CloseSession` is called,
- **Then** the FFmpeg subprocess is killed (`SIGTERM` then `SIGKILL`
  after 2 s grace), the per-session HLS dir is purged, the
  `streaming_sessions` row is updated `closed_at = now(), reason='api'`,
  and the response is empty.

**AC-3 — EvictHashCache.**
- **Given** a `content_hash`,
- **When** `EvictHashCache` is called,
- **Then** every cache entry keyed by that hash (remux, posters,
  sprites, thumbs) is deleted; in-flight sessions reading those files
  are unaffected (kernel keeps the inode alive). Used by Pipeline after
  reprocess.

**AC-4 — HealthCheck.**
- **Given** a HealthCheck request,
- **When** processed,
- **Then** returns `{status, ffmpeg_available, hwaccel, transcode_slots:
  {used, capacity}, cache_used_gib, cache_cap_gib, last_error?}`.

**Test cases:**
- Integration: Open → manifest path exists within 5 s for a transcode
  session against a fixture.
- Integration: Close kills FFmpeg within 2 s grace.
- Integration: EvictHashCache deletes only the specified hash's entries
  (cross-hash isolation).
- Integration: Open with an unknown video id → `NOT_FOUND` gRPC error.

**Edge cases:**
- Open while transcode slots are full — return `RESOURCE_EXHAUSTED`; API
  surfaces 503 to the client (story 7.10).
- Close on an already-closed session — idempotent, returns OK.
- Close on a session whose FFmpeg has already crashed — also idempotent.
- EvictHashCache while a session is reading the file — the OS keeps the
  inode; the file is unlinked from the directory but the read continues.
  The next session for the same hash will see no cache and regenerate.

---

### Story 8.9 — Session store, sticky transcoder, reaper

`streaming_sessions` table holds session metadata. Each session is pinned
to one Streaming process (the one that owns the FFmpeg). In multi-host
deployments, sticky routing (consistent-hash on `session_id` cookie)
keeps the player coming back to the same box per §10.3.

**AC-1 — Session row shape.**
- **Given** an open session,
- **When** the row is inspected,
- **Then** it has at minimum `{id, video_id, user_id, client_profile,
  mode, format, host, pid, started_at, last_segment_at, closed_at,
  closed_reason}`.

**AC-2 — Last-segment heartbeat.**
- **Given** a player fetching segments,
- **When** any segment is served,
- **Then** `last_segment_at = now()` is updated atomically (batched at
  most once per 5 s per session to avoid a write storm).

**AC-3 — Reaper.**
- **Given** the reaper runs every 30 s,
- **When** it finds sessions with `last_segment_at < now() - 90 s` AND
  `closed_at IS NULL`,
- **Then** the FFmpeg is killed, the cache dir is purged, the row is
  marked `closed_at=now(), closed_reason='idle'`, and a metric
  `sessions_reaped_idle_total` is incremented.

**AC-4 — Cross-host stickiness.**
- **Given** a multi-host deployment,
- **When** a session is opened on host A,
- **Then** subsequent segment requests with the session's signed URL must
  reach host A. Achieved via a sticky cookie set on the manifest response
  + L7 LB consistent-hash policy. Misrouted requests return 421
  `Misdirected Request` so the LB can re-route.

**Test cases:**
- Unit: heartbeat batching — 100 segment fetches in 1 s produce 1 DB
  UPDATE.
- Integration: reaper kills an idle session within 30 s of the 90 s
  threshold.
- Integration: a misdirected request to host B for a host-A session
  returns 421 with the canonical-host hint.
- Integration: a closed session's cache dir is gone within 1 s of close.

**Edge cases:**
- A session whose owning Streaming binary crashed — the reaper on any
  Streaming binary that picks up the row (state `last_segment_at` stale
  + no PID match) marks it `closed_reason='crash'` and tries to clean
  the local cache dir. Cross-host cache cleanup is skipped (each box
  cleans its own).
- Player paused for > 90 s — session is reaped; on play resume the
  player must reopen the session (web player auto-detects 401 on next
  segment and calls `POST /api/stream/sessions`).
- `last_segment_at` updates colliding with reaper read — reaper uses
  `SELECT … FOR UPDATE SKIP LOCKED` against rows whose `last_segment_at <
  threshold`, so a fresh write between read and update simply means the
  next reaper tick picks it up.

---

### Story 8.10 — Concurrency caps and backpressure

Per §10.4: per-host max concurrent transcodes defaults to
`(num_cores / 4)`. New sessions over the cap fall back to direct play
with a quality cap or queue with "starting soon" UI hint.

**AC-1 — Slot accounting.**
- **Given** a host with `max_transcode = 4`,
- **When** four sessions are open in transcode mode,
- **Then** the fifth `OpenSession` (transcode-required) returns
  `RESOURCE_EXHAUSTED` with `details.suggested_action="queue"|"direct-cap"`.

**AC-2 — Direct-cap fallback.**
- **Given** the source can be direct-played at 720p (downsampling not
  required) but the matrix recommended transcode for adaptive,
- **When** slots are full,
- **Then** the session is opened in direct mode capped at 720p, and the
  response carries `mode="direct-degraded"`.

**AC-3 — Queue mode.**
- **Given** the request is queue-tolerant (web player passes
  `accept_queue=true`),
- **When** slots are full,
- **Then** the session is recorded with `state='queued'` and the API
  returns 202 with `position` and `eta_sec`. When a slot frees, the
  next queued session is promoted; the API notifies the client over WS
  (extends story 7.10).

**Test cases:**
- Integration: open 5 transcode sessions on a 16-core host → 4 succeed,
  5th queues; closing one promotes the queued session within 5 s.
- Integration: under cap pressure, direct-playable videos still open
  even when transcode is exhausted.

**Edge cases:**
- Cap lowered at runtime via settings → existing sessions are *not*
  killed; new ones respect the new cap. Documented in operations.
- A queued session whose client disconnects before promotion is reaped
  by the queue cleaner (every 30 s) and counted in
  `queued_sessions_abandoned_total`.

---

### Story 8.11 — Live subtitle rendering

Three sources, all served as VTT to the player (§4.5). Auto-generated
subs are rendered live from `transcript_segments` so they appear before
transcription is complete.

**AC-1 — Auto-generated VTT, live from DB.**
- **Given** a session whose video has `transcript_segments` rows,
- **When** `GET /stream/{session_id}/subs/auto.vtt` is fetched,
- **Then** the response is a valid WebVTT file generated at request time
  by streaming over `transcript_segments` (paginated to avoid memory
  spikes) and applying the §3.5 formatting rules. Cache-Control:
  `no-cache, must-revalidate` (the transcript can grow under the
  player's feet).

**AC-2 — Sidecar SRT/VTT served as VTT.**
- **Given** a sidecar `.srt` next to the video,
- **When** `GET /stream/{session_id}/subs/{lang}.vtt` is fetched,
- **Then** the SRT is converted to VTT on first request, cached at
  `cache/subs/{hash}/{lang}.vtt`, and served.

**AC-3 — Embedded subtitle extraction.**
- **Given** a video with embedded `S_TEXT/UTF8`,
- **When** the matching subtitle URL is fetched for the first time,
- **Then** `ffmpeg -map 0:s:N -c:s webvtt` extracts the track to the
  cache, and subsequent requests hit the cache.

**AC-4 — HLS subtitle playlist wrapper.**
- **Given** any subtitle source,
- **When** `GET /stream/{session_id}/subs/{lang}.m3u8` is fetched,
- **Then** the response is a single-segment HLS subtitle playlist
  pointing at the VTT, with `EXT-X-TARGETDURATION` covering the full
  video duration.

**AC-5 — Bidi safety in cues.**
- **Given** mixed-script transcript text,
- **When** rendered to VTT cues,
- **Then** each cue's text is bidi-isolated as in story 7.6 and lines
  wrap at the source language's natural break points (Arabic punctuation
  preferred for Arabic source; etc.).

**Test cases:**
- Integration: auto-VTT for an in-flight transcript that grows mid-fetch
  → the response contains the segments present at the moment of
  generation; a refetch after 10 s contains more.
- Integration: SRT→VTT round trip preserves timestamps to ms precision.
- Integration: embedded MKV subtitle extraction is single-flight under
  concurrent fetches.
- Integration: validates against W3C WebVTT validator on a fixture set.

**Edge cases:**
- Transcript empty (transcribe job hasn't started) — return a valid
  empty WebVTT (`WEBVTT\n\n`) so the player initializes the track.
- Subtitle longer than the video (wrong sidecar) — clip cues to
  `duration_sec`; log a warning.
- Embedded subtitle in an obscure format (e.g. PGS image-based) —
  conversion to VTT is impossible; the URL responds 415
  `type: subtitle-format-unsupported` and the API filters such tracks
  out of `GET /api/videos/{id}/subtitles`.
- A user requests burned-in subtitles (`burn_subs=true` on session
  open) — the transcoder applies `-vf "subtitles=...:force_style=..."`;
  this story's URL serves nothing (the burn happens in story 8.5's
  pipeline). Document the cross-link.

---

### Story 8.12 — Chapter delivery

§4.6 chapter sources unified into one `GET /stream/{session_id}/chapters.json`
plus `#EXT-X-DATERANGE` markers in the master playlist for HLS-aware
players.

**AC-1 — JSON endpoint.**
- **Given** a session,
- **When** `chapters.json` is fetched,
- **Then** the response is `[{seq, start_sec, end_sec, title, source}]`
  sorted by `start_sec`. Sources merge in priority `embedded > manual >
  inferred`.

**AC-2 — DATERANGE in HLS.**
- **Given** the master playlist is built,
- **When** chapters exist,
- **Then** one `#EXT-X-DATERANGE:CLASS="chapter",ID="<seq>",START-DATE=...,
  DURATION=...,X-TITLE="..."` tag is emitted per chapter, anchored to the
  session's `started_at` so DATERANGE math works.

**Test cases:**
- Unit: priority merge — three chapters from each source with
  overlapping ranges → embedded wins on overlap.
- Integration: AVPlayer's `AVPlayerItemChapterMetadata` populates from
  the playlist DATERANGE.
- Integration: a session with no chapters returns `[]` and the playlist
  has no DATERANGE entries.

**Edge cases:**
- Two chapters with identical `start_sec` — secondary sort by `seq`.
- Chapter `end_sec` > `duration_sec` — clamped to `duration_sec`.
- An "inferred" chapter at a place where the player has no segment yet
  (live HLS window) — it's still in the JSON; the DATERANGE in the live
  playlist is added when the segment containing it is in the rolling
  window.

---

### Story 8.13 — Posters, sprite sheets, chapter thumbs

Static assets generated by the Pipeline and served as plain HTTP per
§4.9.

**AC-1 — Poster.**
- **Given** `GET /stream/posters/{video_id}.jpg`,
- **When** the file exists in `cache/posters/{hash[:2]}/{hash}.jpg`,
- **Then** it's served with `Cache-Control: public, max-age=2592000,
  immutable`, `Content-Type: image/jpeg`.
- **When** missing (Pipeline hasn't run thumbnail stage), returns 404.

**AC-2 — Sprite sheet + VTT.**
- **Given** `GET /stream/sprites/{video_id}.webp` and `.vtt`,
- **When** both exist,
- **Then** they're served similarly. The `.vtt` references the sprite
  by relative URL; the player consumes both for scrub previews.

**AC-3 — Chapter thumbs.**
- **Given** `GET /stream/thumbs/{video_id}/chapter-{n}.jpg`,
- **When** the file exists,
- **Then** it's served; missing → 404.

**Test cases:**
- Integration: ETag/`If-None-Match` correctly returns 304 on
  unchanged poster.
- Integration: serving 100 poster requests in parallel uses zero CPU
  (sendfile path).

**Edge cases:**
- Poster file is 0 bytes (interrupted Pipeline write) — detected by a
  size check; treated as missing → 404; the Pipeline retry will
  regenerate.
- WebP not supported by an ancient client — out of scope; the player
  falls back to no scrub preview.

---

### Story 8.14 — Cache layout and LRU GC

Per §4.8: bounded on-disk cache, default 50 GiB combined across remux,
posters, sprites, thumbs (HLS per-session is excluded from the cap and
purged on close per story 8.9).

**AC-1 — Layout.**
- **Given** the cache root,
- **When** files are written,
- **Then** they live at the §4.8 paths exactly, with two-char hash
  shards to avoid wide directories.

**AC-2 — LRU eviction.**
- **Given** the cache exceeds `max_gib`,
- **When** the GC runs (every 5 min),
- **Then** files are deleted in least-recently-accessed order until
  usage is below `max_gib * 0.9` (10% headroom). atime is read from
  `os.Stat`; on filesystems without atime tracking (`noatime`), the
  GC falls back to mtime + a per-file access counter kept in a small
  bbolt sidecar.

**AC-3 — Per-tier soft caps.**
- **Given** the cache is approaching its cap and 80% of bytes are remux
  files,
- **When** GC runs,
- **Then** remux is preferentially evicted (it's regenerable in seconds);
  posters and sprites have a soft floor of 1 GiB before they're evicted
  (regeneration requires re-running the Pipeline).

**AC-4 — Manual GC.**
- **Given** the operator runs `maktaba-streaming gc`,
- **When** invoked,
- **Then** GC runs immediately and prints `{evicted_files, freed_gib,
  duration_ms}` to stdout.

**Test cases:**
- Integration: fill cache to 60 GiB with 30 GiB cap → GC reduces to 27
  GiB.
- Integration: priority — remux at 8 GiB, posters at 0.5 GiB, cap 5 GiB
  → posters survive, remux evicted.
- Integration: GC respects in-flight reads (file is unlinked but the
  open FD continues to work).

**Edge cases:**
- Cap lowered below current usage at runtime — GC catches up in the
  next tick; aggressive single-pass GC is gated on a flag to avoid IO
  storms.
- Concurrent writes during GC — file is written under a `.tmp` name and
  rename'd atomically; GC ignores `.tmp` files younger than 1 min.
- ENOSPC on cache write — returns 507 `Insufficient Storage`; emits a
  metric; force-runs GC immediately.

---

### Story 8.15 — Probe cache

Per §4 intro: a session lookup needs the file path and probe metadata.
The Streaming Service caches probes in-memory (LRU) and falls back to
Postgres `media_info` (written by Pipeline at the probe stage).

**AC-1 — Cache hit path.**
- **Given** a session for a video whose probe is in the LRU,
- **When** OpenSession reads the metadata,
- **Then** no DB query is issued.

**AC-2 — DB fallback.**
- **Given** a cold cache,
- **When** OpenSession is processed,
- **Then** one `SELECT … FROM videos JOIN media_info … JOIN audio_tracks
  …` query populates the cache and the response.

**AC-3 — On-disk re-probe is forbidden.**
- **Given** a video whose `media_info` row is missing,
- **When** OpenSession is processed,
- **Then** the response is `FAILED_PRECONDITION` with `detail:
  "video-not-probed"`. Streaming never invokes ffprobe itself; the API
  enqueues a probe job (Story 7.5 `/process`) and the user retries.

**Test cases:**
- Integration: 1000 OpenSessions for the same video → 1 DB query.
- Integration: video with missing media_info → FAILED_PRECONDITION.
- Integration: cache eviction after `media_info_cache_size` (default
  10,000 entries) follows LRU.

**Edge cases:**
- `media_info` updated by Pipeline (re-probe after file change) — the
  cache is invalidated by the Pipeline calling Streaming's
  `EvictHashCache` (story 8.8 AC-3) at the same time it invalidates the
  remux cache.
- Concurrent OpenSession for the same uncached video — single-flight
  pattern ensures one DB query, all callers wait.

---

## Epic 9 — Library Management

A library is a named collection of root paths sharing a configuration
profile (§5). This epic owns the long-lived behaviors that turn a folder
on disk into a curated, browsable catalog: filesystem watching, dedup,
auto-categorization, the user-facing organization primitives (collections,
smart collections, tags, speakers), and library-level lifecycle (create,
scan, stats, delete-with-purge).

The split with the rest of the platform:

- The **REST surface** for libraries, collections, tags, and speakers
  lives in Epic 7 (Stories 7.3, 7.14). This epic implements the
  *behavior* behind those handlers.
- The **filesystem watcher** is owned by the Pipeline Service (§5.1) — a
  Python `watchdog` observer per library. This epic implements the
  watcher and the rules around debounce, dedup, and ignore.
- The **transcribe / index pipeline stages** are Epics 1–6 in the
  separate doc; this epic only triggers them.
- **Auto-categorization** runs after `INDEXED` and is its own stage in
  the Pipeline.

### Story map

| #     | Story                                                | Depends on |
|-------|------------------------------------------------------|------------|
| 9.1   | Library config schema and validation                 | —          |
| 9.2   | Filesystem watcher (debounced, settling-aware)       | 9.1        |
| 9.3   | Periodic full sweep (sparse, idempotent)             | 9.2        |
| 9.4   | Content-hash dedup (move/rename/copy detection)      | 9.2        |
| 9.5   | Ignore rules and supported-extension filtering       | 9.2        |
| 9.6   | Manual scan trigger and scan progress                | 9.2, 7.3   |
| 9.7   | Library stats query                                  | 9.1, 7.3   |
| 9.8   | Auto-categorization: language tag                    | 9.1, transcribe stage |
| 9.9   | Auto-categorization: topic tag (k-means recluster)   | 9.8, embedder |
| 9.10  | Auto-categorization: content type classifier         | 9.1, probe stage |
| 9.11  | Speakers, voiceprints, naming, merge                 | 9.1, diarization stage |
| 9.12  | Tag CRUD and normalization                           | 9.1        |
| 9.13  | Collections (manual, ordered)                        | 9.1        |
| 9.14  | Smart collections (saved-search-backed)              | 9.13, 7.9  |
| 9.15  | Library deletion (catalog vs file purge)             | 9.1        |
| 9.16  | Multi-root and overlap detection                     | 9.1        |
| 9.17  | Library audit log                                    | 9.1        |

---

### Story 9.1 — Library config schema and validation

The `libraries.settings` JSONB blob is the per-library config profile
(§5). This story locks the schema, the merge semantics, and the
boot-time validation.

**AC-1 — Schema enforcement.**
- **Given** a `settings` blob,
- **When** validated,
- **Then** the following keys are recognized: `language`
  (`auto`|ISO-639-1), `multi_audio` (bool, default false), `stt`
  (`{backend, model, profile, initial_prompt?, max_usd_per_month?}`),
  `embedding` (`{model, device}`), `diarize` (bool), `chapter_inference`
  (bool), `auto_tag_topics` (bool), `default_subtitle_lang` (ISO-639-1),
  `ignore_globs` (string[]). Unknown keys are stored but a warning is
  emitted to the API response on PATCH.

**AC-2 — Defaults inheritance.**
- **Given** a library with `stt: {backend: "whisper-mlx"}` only,
- **When** a worker reads the effective config,
- **Then** missing keys are inherited from `[stt.default]` in
  `pipeline.toml` (§11.4), recursively. The library config can override
  any layer below it.

**AC-3 — Settings change triggers re-evaluation.**
- **Given** an update that bumps the STT model,
- **When** PATCH succeeds,
- **Then** a `library.settings_changed` NOTIFY fires; the orchestrator
  marks newly-arriving videos with the new model from this point. Existing
  videos are *not* re-processed automatically — the user must trigger
  reprocess (Story 7.5).

**Test cases:**
- Unit: every recognized key has a positive + negative validation test.
- Unit: deep-merge with `[stt.default]` fixture produces expected
  effective config.
- Integration: a malformed `stt.backend = "invalid"` returns 422 from
  the PATCH handler with the offending path.

**Edge cases:**
- A library written with a future schema version (forward compat) — keys
  are preserved on read; unknown keys round-trip unchanged.
- A `language` change does not retroactively re-tag old videos. Document
  this in the API reference (and add an action button in the UI for
  bulk re-tag — out of scope here).

---

### Story 9.2 — Filesystem watcher

Per-library `watchdog` observer in the Pipeline Service. The single
hardest piece is *not picking up files mid-write*.

**AC-1 — Debounced event emission.**
- **Given** a `watchdog` event for a path,
- **When** the watcher receives it,
- **Then** the event is queued; if no further event for the same path
  arrives within `watch_debounce_sec` (default 2 s) and the file's size
  has been stable for that interval, an `enqueue-scan` job is created.

**AC-2 — Settling check.**
- **Given** a copy in progress (size grows by N bytes/s),
- **When** the watcher queries size at debounce-tick time,
- **Then** the file is *not* enqueued until two consecutive ticks see the
  same size. Files modified within the last `watch_settle_sec` (default
  5 s) are re-checked rather than enqueued.

**AC-3 — Move detection within a library.**
- **Given** a file moved within the same library root (and the OS emits
  paired `deleted` + `created` events with the same inode on Linux, or a
  `moved` event on macOS),
- **When** the watcher processes the pair,
- **Then** the existing `videos.path` is updated; no scan job is
  enqueued; no derived data is recomputed.

**AC-4 — Watcher restart resilience.**
- **Given** the Pipeline restarts while files were added during downtime,
- **When** the watcher boots,
- **Then** a one-shot full sweep (Story 9.3) catches up, and the watcher
  begins emitting events for further changes. No "missed-during-downtime"
  hole.

**Test cases:**
- Unit: debounce queue collapses N events for the same path within the
  window into one enqueue.
- Integration: simulate a 100 MiB copy completing over 3 s — the file is
  enqueued exactly once, after copy completes.
- Integration: rename across roots within one library — single update
  to `videos.path`; no scan.
- Integration: rename across libraries — treated as delete+add (because
  `library_id` changes); old derived rows are cascaded by FK.

**Edge cases:**
- File created and deleted within the debounce window — never enqueued;
  the watcher cancels the pending tick.
- A file system that doesn't emit reliable events (some FUSE mounts) —
  the periodic full sweep (Story 9.3) is the backstop. Document the
  failure mode in operations.
- Massive `mv` of 10,000 files at once — the watcher coalesces by
  parent dir; the orchestrator picks them up over time, capped by the
  scan stage's concurrency (4 by §7.4).
- Symlink loops in a root — followed by `watchdog`'s `recursive` mode but
  must be guarded; we use a per-scan visited-inode set to prevent
  infinite recursion.

---

### Story 9.3 — Periodic full sweep

A sparse periodic walk (default every 6 h per §3.1) that catches anything
the live watcher missed: NFS event drops, mount remounts, files moved in
while Pipeline was down.

**AC-1 — Diff against catalog.**
- **Given** a library root and the current `videos` catalog,
- **When** the sweep runs,
- **Then** for each file: if `(path, size, mtime)` matches an existing
  row, skip; if `path` is new but a row with the same `content_hash`
  exists at a different path, treat as a move (update `path`); else
  enqueue a `scan` job.

**AC-2 — Sweep is single-flight.**
- **Given** a sweep is in progress,
- **When** the next tick fires before completion,
- **Then** the new tick is dropped (logged at info). No two concurrent
  sweeps.

**AC-3 — Configurable interval.**
- **Given** a library with `sweep_interval_sec` set,
- **When** the scheduler runs,
- **Then** the per-library interval overrides the default. `0` disables
  periodic sweeps (manual scan only).

**AC-4 — Sweep telemetry.**
- **Given** any sweep,
- **When** complete,
- **Then** a row is written to `library_sweeps (library_id, started_at,
  finished_at, scanned, new_videos, moved_videos, removed_videos,
  errors_jsonb)`. Surfaced via `GET /api/libraries/{id}/stats`.

**Test cases:**
- Integration: 100k-file fixture (mostly already-indexed) completes in
  under 30 s on a local SSD (size+mtime cheap path).
- Integration: a deleted file is detected and the matching row is
  marked `state='missing'` (not deleted; user must purge).

**Edge cases:**
- A file whose size+mtime matches but BLAKE3 has changed (rare, unless
  a tool rewrites preserving mtime) — the size+mtime fast path may miss
  this. The user can force a hash-rescan via `POST /api/libraries/{id}/scan?rehash=true`.
- A NAS mount that takes 30 s to wake up — the sweep blocks; the
  watcher buffers events.
- Two libraries with overlapping roots (rejected at create per Story
  7.3 AC-1) — guarantees this story doesn't see the same file twice.

---

### Story 9.4 — Content-hash dedup

Identity is BLAKE3 over first 4 MiB + last 4 MiB + size (§3.1, §1.5). A
new file whose hash already exists is treated as a copy/move/rename.

**AC-1 — Hash computation.**
- **Given** a file ≥ 8 MiB,
- **When** the hasher runs,
- **Then** the hash is computed over `[0..4MiB) + [size-4MiB..size) +
  size_bytes_le`. Files smaller than 8 MiB are hashed in full.

**AC-2 — Hash uniqueness.**
- **Given** two files with the same hash,
- **When** both are seen by the scanner,
- **Then** only the first inserts a `videos` row; the second updates
  `path` to the most-recently-seen path (the older path is discarded; if
  both files coexist on disk, the catalog points to one and the other is
  recorded in `library_audit (action="duplicate", path)`).

**AC-3 — Performance.**
- **Given** a 30 GiB file on local SSD,
- **When** hashed,
- **Then** the operation completes in under 100 ms (8 MiB read + small
  CPU). Network filesystems must respect a `hash_timeout_sec` (default
  30 s) and skip-with-error if exceeded.

**Test cases:**
- Unit: identical files in different folders produce the same hash.
- Unit: byte-for-byte different files in the [4 MiB..size-4 MiB) range
  produce the *same* hash (this is a known property; the test documents
  it so reviewers don't think it's a bug).
- Integration: hash a 50 GiB file in a CI fixture (synthesized) under
  the time budget.

**Edge cases:**
- Files exactly 8 MiB — hashed in full (tiny optimization).
- Truncated read on EOF in the last-4-MiB window — the partial bytes are
  hashed; this is consistent for the same file across reads.
- Hash collision — astronomically unlikely with BLAKE3; if observed,
  the catalog preserves the first-seen entry and logs a `hash_collision`
  metric. The user can force a re-process to verify.

---

### Story 9.5 — Ignore rules and extension filtering

Per §3.1: hidden files, partial downloads, sidecar dirs, and unsupported
extensions are skipped. User-configurable `ignore_globs` extends this.

**AC-1 — Built-in ignores.**
- **Given** any scan,
- **When** a path matches `**/.*`, `**/*.part`, `**/*.crdownload`,
  `**/.maktaba/**`, `**/.DS_Store`, `**/Thumbs.db`,
- **Then** it is skipped silently.

**AC-2 — Supported extensions.**
- **Given** a file whose extension is in `supported_video_exts` (default:
  mp4, mkv, mov, m4v, avi, wmv, flv, webm, mpeg, mpg, ts, m2ts, mts,
  vob, ogv, 3gp),
- **When** scanned,
- **Then** it is enqueued for probe.
- **Given** an extension not in the set, **Then** it's skipped.

**AC-3 — User globs.**
- **Given** a library with `ignore_globs: ["**/raw/**", "**/*.tmp.mp4"]`,
- **When** a matching file is encountered,
- **Then** it is skipped. `ignore_globs` is also applied to the watcher
  (live events are filtered before debounce).

**Test cases:**
- Unit: each built-in pattern with a positive + negative case.
- Unit: case-insensitive match on Windows; case-sensitive on Linux/macOS.
- Integration: changing `ignore_globs` after files are already indexed
  does not retroactively remove them — the user must purge.

**Edge cases:**
- `.maktaba/` is the sidecar root and must be ignored even at deep
  nesting (`/library/sub/.maktaba/...`). The pattern uses `**/.maktaba/**`,
  not `.maktaba/**`.
- A user adds `**/*` to `ignore_globs` — every scan becomes a no-op;
  documented as a way to "freeze" a library without deleting it.
- An unknown video extension that ffprobe could actually decode — the
  user must add it to `supported_video_exts` in app settings; no
  auto-detection.

---

### Story 9.6 — Manual scan trigger and scan progress

`POST /api/libraries/{id}/scan` is the user-initiated entry point
(§9.1). This story defines the job's progress reporting and the
`?rehash=true` mode.

**AC-1 — Default mode.**
- **Given** a manual scan request,
- **When** processed,
- **Then** a `scan` job is enqueued at priority 50 (Story 7.3 AC-5),
  the worker walks the roots, applies the size+mtime fast path of
  Story 9.3, and only computes BLAKE3 for new/changed files.

**AC-2 — Rehash mode.**
- **Given** `?rehash=true`,
- **When** processed,
- **Then** every file is re-hashed regardless of size+mtime, and a
  `videos` row whose hash no longer matches the file is split into a
  new row + the old row marked `state='superseded'`. Used after a
  filesystem corruption or a tool that rewrote files in place.

**AC-3 — Progress reporting.**
- **Given** a scan in flight,
- **When** the worker updates progress,
- **Then** `processing_jobs.processed_seconds` is repurposed to mean
  "files scanned"; `total_duration_seconds` to mean "files to scan"
  (estimated via a fast `find` count first). The §7.10 WS event shape is
  preserved.

**Test cases:**
- Integration: a scan over 1000 files reports progress at 1 Hz to the WS.
- Integration: `?rehash=true` against a corrupted file detects the hash
  mismatch and supersedes correctly.

**Edge cases:**
- Scan started while watcher events are in-flight — both processes update
  the same `videos` table; an `INSERT … ON CONFLICT (content_hash) DO
  UPDATE SET path = EXCLUDED.path` handles the race deterministically.
- Scan canceled via Story 7.12 — the in-progress walk stops at the next
  file boundary; partial progress is preserved (rows already inserted
  remain).

---

### Story 9.7 — Library stats query

`GET /api/libraries/{id}/stats` (Story 7.3 AC-6 binds the endpoint; this
story defines the *contents* and the SQL).

**AC-1 — Composition.**
- **Given** a library,
- **When** stats are requested,
- **Then** the response includes:
  ```
  total_videos
  total_duration_sec
  by_state: {discovered, probed, transcribed, indexed, ready, failed, missing, superseded}
  processed_pct = ready / total
  by_language: { "ar": N, "en": M, "und": K, ... }
  by_content_type: { "lecture": N, "sermon": M, ... }
  storage:
    source_size_bytes
    derived_size_bytes      (transcripts + subtitles + sidecars)
  jobs:
    pending, running, paused, failed
  last_sweep: { started_at, finished_at, scanned, new_videos }
  ```

**AC-2 — Single-query performance.**
- **Given** a 50,000-video library,
- **When** stats are requested,
- **Then** the SQL completes in under 50 ms (counts via `GROUP BY` over
  indexed columns; storage via cached aggregates updated on insert/delete).

**Test cases:**
- Integration: counts add up to `total_videos` for `by_state` and
  `by_language`.
- Integration: `processed_pct` rounds to 2 decimals.
- Performance: stats query under the 50 ms budget on the perf fixture.

**Edge cases:**
- Empty library — every count is 0; `processed_pct = null` (not 0/0).
- Stats requested while the library is being deleted — return 404 if
  the row is gone; otherwise return whatever is current.

---

### Story 9.8 — Auto-categorization: language tag

After `TRANSCRIBED`, the language detected by Whisper is written to
`videos.detected_language` (§5.2).

**AC-1 — Single-language assignment.**
- **Given** a transcript with `language='ar'` from STT,
- **When** the transcribe stage completes,
- **Then** `videos.detected_language = 'ar'` in the same transaction
  that flips state.

**AC-2 — Multi-audio overrides.**
- **Given** a video with multiple audio tracks transcribed (library
  `multi_audio=true`),
- **When** stats are computed,
- **Then** the *primary* track's language goes on `videos.detected_language`;
  per-track languages live on the transcripts rows.

**AC-3 — Confidence threshold.**
- **Given** Whisper's language detection confidence < 0.6,
- **When** assigning,
- **Then** `detected_language = 'und'` (undetermined); the user can
  manually set it via `PATCH /api/videos/{id}` (extends Story 7.4).

**Test cases:**
- Unit: low-confidence fixture → `und`.
- Integration: PATCH user-set language overrides STT's value and is
  preserved across re-processing.

**Edge cases:**
- A library with `language: "ar"` (forced) — the user-pinned value is
  always written, regardless of STT confidence (the user knows their
  archive better than Whisper does).
- A code-switched audio (Arabic-English mix) — Whisper picks one;
  cross-language search via Chroma still works because embeddings are
  multilingual.

---

### Story 9.9 — Auto-categorization: topic tag

After `INDEXED`, each video is tagged with its top-K nearest cluster
centroids in the library's vector space; clusters are recomputed nightly
(§5.2).

**AC-1 — Per-library cluster set.**
- **Given** a library with ≥100 indexed videos,
- **When** the nightly recluster runs,
- **Then** mini-batch k-means computes K clusters (default
  `topic_clusters = sqrt(N)/2`, capped at 32) over per-video mean
  embeddings, and a `library_topics (library_id, topic_id, label?,
  centroid_vec, video_count)` row is upserted per cluster.

**AC-2 — Topic labeling.**
- **Given** a freshly-formed cluster,
- **When** the labeler runs,
- **Then** the top-5 segments closest to the centroid are concatenated
  and asked from the embedder for nearest token — the resulting bigram is
  the human-readable label (e.g., `"prayer-rituals"`). The user can
  rename via `PATCH /api/libraries/{id}/topics/{topic_id}`.

**AC-3 — Per-video assignment.**
- **Given** a video with a mean embedding,
- **When** scored against the library's centroids,
- **Then** the top-3 nearest topics by cosine similarity are stored in
  `video_topics (video_id, topic_id, score)`.

**AC-4 — Disabled by setting.**
- **Given** library setting `auto_tag_topics: false`,
- **When** the recluster runs,
- **Then** the library is skipped (its `library_topics` rows are
  preserved but unused).

**Test cases:**
- Unit: k-means with a deterministic seed produces stable cluster ids
  across runs given identical input.
- Integration: a fixture with 200 videos in 4 obvious clusters → K=14
  centroids form, the 4 dominant ones contain ~80% of videos.
- Integration: relabeling a topic via PATCH propagates to UI immediately.

**Edge cases:**
- Library with < 100 videos — recluster is skipped (insufficient data);
  topics remain empty until threshold is crossed.
- A video with no transcript yet — no topic assignment; the video appears
  under "untagged".
- A new video added between recluster nightly runs — assigned topics
  using existing centroids on the next index commit; the centroids
  drift over the next recluster.

---

### Story 9.10 — Auto-categorization: content type classifier

A small classifier (§5.2) predicts `content_type ∈ {lecture, sermon,
interview, film, music_video, unknown}` from features computed during
probe and audio extract: duration, speaker turn density (from
diarization if on, segment density otherwise), and music-vs-speech ratio
from `silencedetect` + `loudnorm` stats.

**AC-1 — Feature extraction during probe.**
- **Given** the probe stage,
- **When** it completes,
- **Then** `media_features (video_id, music_speech_ratio,
  silence_pct, mean_loudness_lufs, diarization_turn_density,
  segment_density)` is populated.

**AC-2 — Classifier inference.**
- **Given** a row in `media_features` and the trained model,
- **When** the categorize stage runs,
- **Then** `videos.content_type` is set to the argmax class with
  confidence; if confidence < 0.55 → `unknown`.

**AC-3 — Manual override.**
- **Given** a user sets `content_type` via PATCH,
- **When** auto-classifier runs again (e.g., after re-probe),
- **Then** the user value is preserved unless `?force=true` is set.

**Test cases:**
- Unit: deterministic classifier output for a 5-fixture set covering each
  class.
- Integration: a 90-min film fixture → `film`; a 45-min sermon fixture
  → `sermon`.
- Integration: per-user override sticks across reprocess.

**Edge cases:**
- Music-heavy video (concert) classified as `music_video` even when it
  has speech intros — score is from the dominant class.
- An ultra-short clip (< 60 s) — confidence floor isn't met → `unknown`.

---

### Story 9.11 — Speakers, voiceprints, naming, merge

§5.2 + §9.6 endpoints. Diarization is opt-in per library; when on,
voiceprints are matched against per-library `speakers`.

**AC-1 — New voice → unknown speaker.**
- **Given** diarization detects a voice not matching any existing
  `speakers.voiceprint` (cosine distance > `speaker_match_threshold`,
  default 0.35),
- **When** the segment is committed,
- **Then** a new `speakers (library_id, name=NULL, voiceprint)` row is
  created with `name = "unknown-{n}"` rendered in the UI; n is the
  count of unknowns + 1.

**AC-2 — Match assignment.**
- **Given** a new segment whose voiceprint matches an existing speaker
  within threshold,
- **When** committed,
- **Then** `segment_speakers (segment_id, speaker_id, confidence)`
  is inserted; the speaker's voiceprint is *not* updated (avoid drift).

**AC-3 — User naming.**
- **Given** an unknown speaker,
- **When** the user PATCHes a name via Story 7.14 endpoint,
- **Then** the name is set; UI relabels every prior segment by reference.

**AC-4 — Merge.**
- **Given** two speakers found to be the same person,
- **When** `POST /api/speakers/merge {keep, drop}` is called,
- **Then** as in Story 7.14 AC-4: `segment_speakers` rows are rewritten
  in one transaction. The voiceprint of the merged speaker is *not*
  recomputed; it remains the kept speaker's original.

**AC-5 — Cross-library isolation.**
- **Given** speakers in two libraries,
- **When** queried,
- **Then** they never collide; the same person watched across libraries
  is two separate `speakers` rows. No cross-library merge in v1.

**Test cases:**
- Integration: insert 100 segments from 3 voices → 3 speaker rows; merge
  two → 2 rows; rename → 2 rows with names.
- Integration: a voice present in 50 segments named via PATCH → all 50
  segments now display the new name in the next API read.

**Edge cases:**
- Diarization disabled mid-library — existing speakers and `segment_speakers`
  are preserved; new segments simply have no speaker. No data loss.
- Voiceprint storage size — a `d-vector` of 512 floats = 2 KiB per
  speaker; even 10k speakers per library is 20 MiB. Stored as `BYTEA`.
- Two unknown speakers later turn out to be the same — the merge handles
  it; the count of unknowns can decrease (next new unknown takes the
  lowest free index).

---

### Story 9.12 — Tag CRUD and normalization

`tags` and `video_tags` (§8.2). The endpoints are in Story 7.14; this
story owns the normalization and uniqueness rules.

**AC-1 — Normalization on insert.**
- **Given** a tag name `"  Tafsir  "`,
- **When** inserted,
- **Then** the stored `display_name` is `"Tafsir"` (trim), the
  `normalized_name` (a hidden uniqueness key) is `"tafsir"` (NFC unicode
  normalize + casefold).

**AC-2 — Conflict on normalized collision.**
- **Given** an existing tag `"Tafsir"`,
- **When** `"tafsir"` is inserted,
- **Then** the existing row is reused (same `id`); no new row, no
  error. The display name is *not* overwritten.

**AC-3 — Rename preserves links.**
- **Given** a tag with N video links,
- **When** PATCH renames the display name,
- **Then** `normalized_name` is recomputed; if the new normalized form
  collides with another tag, return 409 `type: tag-name-exists` and
  suggest merge. Otherwise the tag id stays; all video links stay.

**Test cases:**
- Unit: normalization fixtures for Arabic (with diacritics, NFC vs NFD).
- Integration: insert two normalize-equal tags → one row.
- Integration: rename to colliding name → 409.

**Edge cases:**
- Empty / whitespace-only tag → 422.
- Tag containing a slash (`"finance/2024"`) — allowed; surfaced as a
  flat string in v1. Hierarchical tags out of scope.

---

### Story 9.13 — Collections (manual ordered)

`collections (is_smart=false)` + `collection_items` (§8.2). Endpoints in
Story 7.14.

**AC-1 — Ordered insertion.**
- **Given** an empty collection,
- **When** items are added with `position` 10, 20, 30,
- **Then** they read back in that order. Re-ordering one item is a single
  UPDATE.

**AC-2 — Insertion at end.**
- **Given** a POST without `position`,
- **When** processed,
- **Then** position = `MAX(position) + 10` (sparse to avoid renumbers).

**AC-3 — Compaction.**
- **Given** drift over time produces `position` values up to 1e9,
- **When** the operator runs `maktaba-api compact-collections`,
- **Then** positions are renumbered 10, 20, 30, … per collection. Online
  reads continue to work; ordering is preserved.

**Test cases:**
- Integration: 100-item collection re-ordered by drag-and-drop (10 single
  UPDATEs) → final order is read back correctly.
- Integration: compaction is idempotent (running twice yields the same
  positions).

**Edge cases:**
- Cycle prevention not applicable (collections are flat).
- Same video added twice — primary key on `(collection_id, video_id)`
  rejects with 409.

---

### Story 9.14 — Smart collections

`collections (is_smart=true, smart_query JSONB)` + Story 7.9 storage.
Items are computed live from `smart_query` per Story 7.14 AC-2.

**AC-1 — Filter shape compatibility.**
- **Given** the same `smart_query` JSON as a saved search,
- **When** materialized,
- **Then** the result set equals the search result set. The two features
  share one filter language and one resolver.

**AC-2 — Live computation.**
- **Given** a smart collection,
- **When** `GET /api/collections/{id}/items` is called,
- **Then** the items are computed from `smart_query` at request time;
  no caching of items; respect cursor pagination from Story 7.2.

**AC-3 — Conversion to manual.**
- **Given** a smart collection,
- **When** the user clicks "freeze" (POST `/convert?freeze=true`),
- **Then** the current item set is materialized into `collection_items`
  in order, `is_smart` flips to false, `smart_query` is moved to
  `frozen_from_query` for audit.

**Test cases:**
- Integration: smart collection's items respond identically to the
  search endpoint with the same query.
- Integration: freeze materializes and a subsequent insert in the
  underlying catalog does *not* affect the frozen collection.

**Edge cases:**
- A smart collection backed by a query that returns 100k items —
  pagination must hold; no in-memory materialization.
- Settings change that invalidates a smart query (e.g. removed a tag) —
  the live computation returns 200 with `items: []` and `warning`
  (matches Story 7.14 AC).

---

### Story 9.15 — Library deletion

Story 7.3 AC-4 binds the endpoint with `?purge`; this story defines the
on-disk semantics and the audit trail.

**AC-1 — Catalog deletion (default).**
- **Given** `DELETE /api/libraries/{id}` (`?purge=false`),
- **When** processed,
- **Then** the library row is deleted in one transaction; FK cascades
  remove `videos`, `media_info`, `audio_tracks`, `transcripts`,
  `transcript_segments`, `chapters`, `subtitle_files`, `playback_state`,
  `collection_items`, `video_tags`, `library_topics`, `library_sweeps`,
  and `streaming_sessions` (closing each first).

**AC-2 — File purge (`?purge=true`).**
- **Given** the purge flag,
- **When** the catalog deletion succeeds,
- **Then** for each root, every file matching `supported_video_exts`
  (and not in `ignore_globs`) is unlinked. Sidecar `.maktaba/` dirs at
  each root are also unlinked. The audit log captures `(action="purge",
  by_user, root, file_count, freed_bytes)`.

**AC-3 — Atomicity.**
- **Given** the catalog delete succeeds but a file unlink fails,
- **When** processed,
- **Then** the response is `207 Multi-Status` with the list of
  unlink failures; the catalog is *not* rolled back. The user must
  manually clean the leftover files.

**Test cases:**
- Integration: delete with active streaming sessions → sessions are
  closed first (via gRPC to Streaming), then the catalog is deleted.
- Integration: purge with a read-only file → 207, the file remains, the
  catalog is gone.
- Integration: dry-run mode (`?dry_run=true`) returns the list of files
  that *would* be deleted without touching anything.

**Edge cases:**
- Library has 1M videos — the FK cascade is one DB transaction;
  Postgres handles it but a long lock is taken. Operations doc warns to
  use `pg_terminate_backend` if it stalls.
- Purge while a Pipeline worker is writing a sidecar — the unlink may
  succeed before the write completes; the worker's atomic-rename pattern
  fails harmlessly. No corruption.

---

### Story 9.16 — Multi-root and overlap detection

A library can have N roots (§5). Roots may not overlap with another
library's roots; within a library, multiple roots are independent.

**AC-1 — N roots in one library.**
- **Given** a library with `roots: ["/mnt/a", "/mnt/b"]`,
- **When** scanned,
- **Then** both trees are walked; results merge into the library's
  catalog. No per-root subdivision in the API.

**AC-2 — Overlap rejection at create/update.**
- **Given** library A with `roots: ["/mnt/media"]`,
- **When** library B is created with `roots: ["/mnt/media/sub"]`,
- **Then** 422 `type: library-roots-overlap` (Story 7.3 AC-2 edge case).

**AC-3 — Overlap detection rule.**
- Two paths overlap if one is a prefix of the other after path
  canonicalization (resolve symlinks, trailing slashes, `..`).

**Test cases:**
- Unit: canonicalization fixtures cover symlink, `..`, trailing slash.
- Integration: `["/a", "/a/b"]` in one library is rejected (a single
  library may not nest its own roots either).

**Edge cases:**
- A symlink that, after resolution, makes two non-overlapping declared
  roots resolve to the same physical path — caught by canonicalization.
- A new mount that, after `mount`, makes a previously non-overlapping
  root overlap — out of scope; not detected at runtime.

---

### Story 9.17 — Library audit log

`library_audit (id, library_id, action, actor_user_id, ts, payload_jsonb)`.
Records lifecycle events: scan triggered, settings changed, video purged,
library deleted, speaker merged, file purge results.

**AC-1 — Append-only.**
- **Given** any audit event,
- **When** written,
- **Then** the row is INSERT-only; updates and deletes are forbidden by
  a `BEFORE UPDATE/DELETE` trigger that raises an exception.

**AC-2 — Surfaced in API.**
- **Given** an admin,
- **When** `GET /api/libraries/{id}/audit?cursor=...` is called,
- **Then** the response is paginated audit entries newest-first.

**AC-3 — Retention.**
- **Given** the audit log grows,
- **When** the nightly trim runs,
- **Then** entries older than `audit_retention_days` (default 365) are
  archived to `library_audit_archive` (a partitioned table) and removed
  from the live table. Archive is read-only via API.

**Test cases:**
- Integration: trying to UPDATE a row → exception; trying to DELETE → exception.
- Integration: trim moves rows older than 365 days into the archive
  table; counts add up.

**Edge cases:**
- Audit log unavailable (DB partial outage) — the actions still succeed
  (audit is best-effort, never blocking); a `audit_write_failed_total`
  metric tracks misses.
- An audit event that contains user-supplied content (e.g., the new
  collection name) — `payload_jsonb` is parameterized; no injection
  risk. Length capped at 8 KiB.

---

## Epic 10 — Auth & Security

Identity, sessions, signed URLs, secret handling, transport hardening.
Two surfaces, one identity (§9.8): web uses httpOnly cookies + CSRF;
mobile/desktop/TV use bearer JWTs + refresh tokens. The Streaming
Service validates JWTs *offline* against the API's published JWKS, so a
playing video survives an API restart (§9.4, §9.8).

**Out of scope for Epic 10:** RBAC beyond admin/viewer (multi-tenant
permissions are a v2 concern), SSO (OIDC, SAML), 2FA. These are noted
where relevant as future hooks but not implemented.

This epic produces the middleware and stores that Epic 7's handlers
consume. It is the first epic to land in any deployable build because
Epic 7 endpoints depend on its `User`, `Session`, and JWT primitives.

### Story map

| #     | Story                                                | Depends on |
|-------|------------------------------------------------------|------------|
| 10.1  | User store + argon2id password hashing               | —          |
| 10.2  | Web login (cookie + CSRF)                            | 10.1       |
| 10.3  | Native login (JWT access + opaque refresh)           | 10.1       |
| 10.4  | Token refresh + rotation                             | 10.3       |
| 10.5  | Logout + session revocation                          | 10.2, 10.3 |
| 10.6  | RS256 key generation, rotation, JWKS publication     | —          |
| 10.7  | Streaming-side offline JWT verification              | 10.6, 8.1  |
| 10.8  | Signed-URL minter (manifest, direct, sidecar)        | 10.6       |
| 10.9  | Single-user mode (admin token bypass)                | 10.1, 10.6 |
| 10.10 | CSRF protection (web only)                           | 10.2       |
| 10.11 | Brute-force / credential-stuffing protection         | 10.2, 10.3 |
| 10.12 | Rate limiting on auth endpoints                      | 10.2, 7.19 |
| 10.13 | Permission model (admin vs viewer; resource scope)   | 10.1       |
| 10.14 | Secret loading and redaction                         | 7.15       |
| 10.15 | Transport security (TLS, HSTS, secure cookies, CORS) | —          |
| 10.16 | Audit log for security-sensitive actions             | 9.17       |

---

### Story 10.1 — User store + argon2id passwords

`users` (§8.5) holds identities. Passwords are argon2id with
configurable memory/time per `[auth]` config (§11.2).

**AC-1 — Hash creation.**
- **Given** a request to set a password,
- **When** the hasher runs,
- **Then** the hash is argon2id with parameters `(memory=65536 KiB,
  time=2, parallelism=1, salt=16 random bytes, hash=32 bytes)` (defaults
  per §11.2). The stored string is the standard `$argon2id$...` PHC
  format including parameters, so future config changes don't invalidate
  existing hashes.

**AC-2 — Constant-time verify.**
- **Given** a stored hash and a candidate password,
- **When** verified,
- **Then** the comparison uses argon2id's built-in constant-time verify
  and never logs the password or hash.

**AC-3 — User CRUD.**
- **Given** an admin,
- **When** `POST /api/users {username, password, is_admin?}` is sent,
- **Then** a row is inserted with the hashed password; the response
  excludes `pw_hash`.
- `PATCH /api/users/{id}` allows changing `username`, `password`,
  `is_admin`. `DELETE /api/users/{id}` cascades to `playback_state`,
  `saved_searches`, refresh-token rows.

**AC-4 — CLI for first user.**
- **Given** an empty `users` table,
- **When** `maktaba-api adduser <username>` is run,
- **Then** the password is prompted (no echo), hashed, and the user is
  inserted with `is_admin=true`. Used in the bootstrap path before any
  HTTP user exists.

**Test cases:**
- Unit: hash verifies; same password produces a different hash (random
  salt); a different password fails verify.
- Unit: argon2 parameters from config thread through to the stored hash
  string.
- Security: a password 1024 chars long is hashed without DoS (capped at
  `password_max_len`, default 256, returning 422 on overflow).

**Edge cases:**
- Username conflict — 409 `type: username-exists`. Compared
  case-insensitively (Unicode casefold) for the uniqueness check;
  display preserves original casing.
- `is_admin` change is allowed only by another admin; a user cannot
  promote themselves.
- Deleting the last admin → 409 `type: last-admin`. The system always
  has at least one admin.

---

### Story 10.2 — Web login (cookie + CSRF)

Web clients log in once and ride a short-lived session cookie.

**AC-1 — Login flow.**
- **Given** valid `username + password`,
- **When** `POST /api/auth/login` is sent (JSON body),
- **Then** the server creates a `web_sessions` row, sets two cookies:
  - `mkt_sess` = opaque session id, `httpOnly`, `secure`, `samesite=lax`,
    `path=/`, `max-age=auth.web_session_ttl_sec` (default 28 days),
  - `mkt_csrf` = random 32-byte token, `secure`, `samesite=lax`, **not**
    `httpOnly` (the SPA reads it),
  and the response body is `{user: {id, username, is_admin}}`.

**AC-2 — Authenticated requests.**
- **Given** a request with a valid `mkt_sess` cookie,
- **When** an authenticated handler runs,
- **Then** the user identity is loaded from the session; the session's
  `last_seen_at` is bumped (debounced to once per minute per session).

**AC-3 — Wrong credentials.**
- **Given** an invalid login,
- **When** processed,
- **Then** the response is `401 Unauthorized` problem+json with a
  generic `type: invalid-credentials` message (don't differentiate
  unknown-user vs wrong-password) and an artificial 500 ms minimum
  delay (timing attack mitigation).

**AC-4 — Session expiry.**
- **Given** a session row whose `created_at + ttl < now()`,
- **When** any request uses it,
- **Then** the request is treated as anonymous (401); the cookie is
  cleared via `Set-Cookie: mkt_sess=; max-age=0`.

**Test cases:**
- Integration: full login flow → cookies set with the correct attributes.
- Integration: tampered `mkt_sess` (changed by 1 char) → 401 + cookie
  cleared.
- Integration: timing attack — user-not-found and wrong-password both
  take ~500 ms (within 50 ms variance).

**Edge cases:**
- Multiple browser tabs: same session cookie shared. Logout in one tab
  invalidates the session for all.
- Cookie with `samesite=lax`: GET cross-site navigation works (deep
  links into Maktaba); POST cross-site does not (CSRF guard, Story
  10.10).
- Reverse proxy strips cookies: documented setup error; the API
  returns 401 with `Maktaba-Hint: cookies-missing-check-proxy`.

---

### Story 10.3 — Native login (JWT access + refresh)

Mobile/desktop/TV clients log in once, then carry a short-lived JWT and
refresh it (§9.8).

**AC-1 — Login.**
- **Given** valid credentials with `Accept: application/json` and
  `X-Maktaba-Client: native`,
- **When** `POST /api/auth/login` is processed,
- **Then** the response is `{access_token: <JWT>, access_expires_in,
  refresh_token: <opaque>, refresh_expires_in, user}`. No cookies set.

**AC-2 — JWT shape.**
- **Given** an issued access token,
- **When** decoded,
- **Then** the claims include `iss="maktaba"`, `aud="api"`, `sub=user_id`,
  `iat`, `exp = iat + 900` (15 min default), `jti=<uuid v7>`, `is_admin`,
  `kid=<key id>`. RS256 signed.

**AC-3 — Bearer auth.**
- **Given** a request with `Authorization: Bearer <jwt>`,
- **When** an authenticated handler runs,
- **Then** the JWT is verified (signature + exp + aud), the `sub` is the
  user, the `jti` is recorded for audit, and the request proceeds.

**AC-4 — Opaque refresh tokens.**
- **Given** a refresh token,
- **When** stored,
- **Then** the token value is a 32-byte url-safe random string; only its
  argon2id hash is persisted in `refresh_tokens (id, user_id, hash,
  family_id, issued_at, expires_at, revoked_at, replaced_by, client_meta)`.
  The plaintext is returned only at issue time.

**Test cases:**
- Integration: login → access token decodes to expected claims.
- Integration: a tampered JWT signature → 401.
- Integration: an expired access token → 401 `type: token-expired`; the
  client is expected to refresh.

**Edge cases:**
- Skewed device clock — the JWT's `iat` may be slightly future to the
  server. Acceptance leeway `clock_skew_leeway_sec` (default 60) on `nbf`
  and `exp`.
- A native client that misses `X-Maktaba-Client: native` and is therefore
  given cookies — this is acceptable; the API supports both flows on
  one endpoint based on the header.

---

### Story 10.4 — Token refresh + rotation

Refresh tokens use rotation: each refresh issues a new refresh token and
invalidates the old one. A reused old token signals theft and revokes
the entire family (§9.8 implied).

**AC-1 — Refresh flow.**
- **Given** a valid refresh token,
- **When** `POST /api/auth/refresh {refresh_token}` is sent,
- **Then** the response is `{access_token, refresh_token, ...}`; the
  used refresh row is marked `revoked_at=now(), replaced_by=<new id>`,
  the new row is inserted with `family_id` inherited.

**AC-2 — Reuse detection.**
- **Given** an already-revoked refresh token (replayed by an attacker),
- **When** presented,
- **Then** every active row in the same `family_id` is revoked; the
  user's other devices are silently logged out; an audit row is written
  `(action="refresh-replay-detected", user_id, family_id, ip)`. The
  response is `401 type: refresh-replayed`.

**AC-3 — Refresh expiry.**
- **Given** a refresh token whose `expires_at < now()`,
- **When** presented,
- **Then** 401 `type: refresh-expired`; the user must log in again. No
  family revocation (this is a normal expiry, not theft).

**Test cases:**
- Integration: refresh → old token now invalid; new token works.
- Security: replay an old refresh after a successful refresh → family
  revoked; previously valid sibling tokens no longer work.
- Integration: expired refresh → 401 expired; the user's other sessions
  are unaffected.

**Edge cases:**
- Network race: client retries refresh before the server's response
  arrives — both requests carry the same old token; the second one
  triggers reuse detection. Mitigation: clients must serialize refresh
  per-device (documented in client SDKs).
- Refresh against a revoked user account — 401, no token issued.
- A device wiped without logout — the refresh token rots until expiry;
  fine.

---

### Story 10.5 — Logout + session revocation

Both surfaces support explicit logout. Admins can revoke any session.

**AC-1 — Web logout.**
- **Given** a logged-in web client,
- **When** `POST /api/auth/logout` is sent with the cookie,
- **Then** the `web_sessions` row is deleted, the response clears
  `mkt_sess` and `mkt_csrf`, status `204`.

**AC-2 — Native logout.**
- **Given** a refresh token,
- **When** `POST /api/auth/logout {refresh_token}` is sent,
- **Then** the matching refresh row is revoked; the access token is
  *not* invalidated server-side (it expires within 15 min naturally).
  The client should drop both tokens immediately.

**AC-3 — Logout-all-devices.**
- **Given** a user,
- **When** `POST /api/auth/logout-all` is sent,
- **Then** every web session and every refresh family for that user is
  revoked; an audit row is written.

**AC-4 — Admin revocation.**
- **Given** an admin,
- **When** `DELETE /api/users/{id}/sessions/{session_id}` is sent,
- **Then** the session is revoked. Same for `refresh_tokens` rows.

**Test cases:**
- Integration: logout → next request with the cookie is 401.
- Integration: logout-all from device A → device B's next refresh fails.
- Integration: admin revokes user X's session; user X is a normal user,
  not admin → user X is logged out.

**Edge cases:**
- Access token still in flight for ~15 min after logout — accepted as
  the price of stateless verification. For high-security needs the
  `access_ttl_sec` can be lowered to 60 s (config knob, documented
  trade-off: more refresh churn).
- Logout while the client has no network — the server has no way to
  expire the token until the client comes back online and POSTs the
  refresh to be told `revoked`.

---

### Story 10.6 — RS256 key generation, rotation, JWKS

The API mints JWTs with a private RS256 key; both the API and Streaming
verify with the public key. Keys rotate without breaking in-flight tokens.

**AC-1 — Key material loading.**
- **Given** `MAKTABA_JWT_PRIVATE_KEY_PEM` and `MAKTABA_JWT_PUBLIC_KEY_PEM`
  env vars,
- **When** the API boots,
- **Then** keys are loaded; the `kid` is the SHA-256 of the public key DER
  truncated to 16 chars; if either key is missing, the API refuses to
  start with a clear error.

**AC-2 — Bootstrap key generation.**
- **Given** an empty install with no key env vars,
- **When** `maktaba-api keys init` is run,
- **Then** a 4096-bit RSA keypair is generated and printed in PEM form,
  with the env-var names that should hold them. The command never writes
  to disk (operator-controlled).

**AC-3 — JWKS publication.**
- **Given** the API is running,
- **When** `GET /api/.well-known/jwks.json` is called,
- **Then** the response is a JWKS containing every `kid` currently
  trusted: at minimum the active signing key and the previous one
  (during rotation). `Cache-Control: public, max-age=300`.

**AC-4 — Key rotation.**
- **Given** the admin runs `maktaba-api keys rotate`,
- **When** processed,
- **Then** a new keypair is generated, its public key is added to the
  JWKS, and after `rotation_overlap_sec` (default 24 h) the old key is
  removed. New tokens are signed with the new key. Old tokens remain
  valid until the overlap ends.

**Test cases:**
- Integration: generated keys verify a token end-to-end.
- Integration: rotation — JWTs signed before rotation continue to verify
  for the overlap window.
- Integration: JWKS endpoint reflects rotation in <1 s.

**Edge cases:**
- A leaked private key — the operator forces rotation with
  `--immediate` (overlap=0), which invalidates every in-flight token.
  Documented as a security incident response.
- Key shorter than 2048 bits → refused at boot with a clear error.
- JWKS endpoint blocked by a firewall — Streaming caches the last-seen
  JWKS indefinitely (Story 8.1 AC-2). Documented in operations.

---

### Story 10.7 — Streaming-side offline JWT verification

Streaming validates JWTs without calling the API (§9.8). Story 8.1
implements the wire format; this story owns the *behavior* of trust:
how Streaming bootstraps and refreshes JWKS, how it handles rotation.

**AC-1 — JWKS bootstrap.**
- **Given** Streaming starts,
- **When** the first JWKS fetch runs,
- **Then** it succeeds within `jwks_initial_timeout_sec` (default 10 s);
  on failure the binary still starts but rejects all signed-URL
  requests with 503 `type: jwks-unavailable` until the first fetch
  succeeds.

**AC-2 — Rotation handling.**
- **Given** the API rotates and adds a new `kid` to the JWKS,
- **When** Streaming refreshes (next 5 min poll),
- **Then** tokens with the new `kid` verify correctly. Tokens with the
  old `kid` continue to verify until the API removes that key from the
  JWKS.

**AC-3 — Audience and issuer checks.**
- **Given** any token,
- **When** verified,
- **Then** `iss="maktaba"`, `aud ∈ {streaming, streaming-direct}` (per
  endpoint class), `exp` not past, `nbf` not future, `kid` in JWKS.
  Mismatch → 401 with the appropriate `type` (Story 8.1).

**Test cases:**
- Integration: end-to-end signed URL from API to Streaming verifies; an
  attacker-signed token with the same shape fails.
- Integration: rotation event → Streaming accepts new tokens within 5
  min (or immediately if `LISTEN jwks_changed` is used as an
  optimization).

**Edge cases:**
- Two API replicas with different active signing keys momentarily during
  a rotation — both keys are in the JWKS; both verify fine. Documented
  as the reason JWKS holds N>1 keys.
- A clock-skew at the Streaming box that puts `now()` 30 s behind the
  API — `exp` leeway absorbs it.

---

### Story 10.8 — Signed-URL minter

The API mints signed URLs for Streaming sessions, direct play, and
subtitles (Stories 7.10, 7.7).

**AC-1 — Manifest URL.**
- **Given** `mintManifestURL(session_id, ttl)`,
- **When** called,
- **Then** the URL is `https://<streaming_origin>/stream/{session_id}/
  manifest.m3u8?sig=<jwt>` where `<jwt>` carries
  `aud=streaming, sub=session_id, exp=now+ttl`. Default ttl is
  `session_url_ttl_sec` (1800 s).

**AC-2 — Direct URL.**
- **Given** `mintDirectURL(video_id, user_id, ttl)`,
- **When** called,
- **Then** the URL carries `aud=streaming-direct, sub=video_id`,
  plus `usr=user_id` for audit, signed with the API's key.

**AC-3 — Sidecar URL (poster, sprite, subtitle).**
- **Given** any read-only artifact for a video,
- **When** a URL is minted,
- **Then** `aud=streaming-static, sub=<artifact-path-hash>`, ttl matches
  the asset's recommended cache lifetime (poster 1 h, subtitle 1 h,
  sprite 1 h).

**AC-4 — TTL is bounded.**
- **Given** any caller,
- **When** ttl is requested above `max_signed_url_ttl_sec` (default
  86400),
- **Then** the value is capped silently and a metric incremented.

**Test cases:**
- Unit: each URL kind decodes to the expected claims.
- Integration: a minted URL is accepted by Streaming until expiry, then
  rejected.

**Edge cases:**
- The API has no private key configured (misconfig) — the minter raises
  a `KeyUnavailable` error; callers translate to 503 `type: signing-unavailable`.
- A URL minted with `aud=streaming` then sent to `/stream/direct/...` →
  rejected with `type: wrong-aud` (Story 8.1 AC-1).

---

### Story 10.9 — Single-user mode (admin token bypass)

The zero-config path for self-hosters (§9.8): an env-configured admin
token bypasses the user table.

**AC-1 — Admin token presence enables bypass.**
- **Given** `MAKTABA_ADMIN_TOKEN` is set,
- **When** any request carries `Authorization: Bearer <that-token>` (or
  cookie `mkt_admin_token=<that-token>`),
- **Then** the request is treated as the synthetic user `(id=ADMIN_UUID,
  username="admin", is_admin=true)` — no DB lookup.

**AC-2 — Constant-time compare.**
- **Given** a candidate token,
- **When** compared to the configured token,
- **Then** the comparison uses constant-time equality (no early exit on
  length or content).

**AC-3 — UI bootstrap.**
- **Given** the user has no other auth configured,
- **When** they first open the web UI,
- **Then** a one-time "paste your admin token" dialog stores the token
  in `localStorage` (and the SPA sends it as a cookie). The user can
  later create real user accounts from settings.

**AC-4 — Token rotation.**
- **Given** the admin restarts the API with a different value of
  `MAKTABA_ADMIN_TOKEN`,
- **When** an old-token request arrives,
- **Then** it is rejected as 401. There is no grace period for env-var
  rotation (this is operator-driven, not user-driven).

**Test cases:**
- Integration: admin token bypass works; an empty env var means no
  bypass (random tokens cannot accidentally match).
- Security: a 1-char-different token is rejected (no early exit).

**Edge cases:**
- Both admin token *and* user table populated — both authentication
  paths work; the admin-token path always lands on the synthetic admin
  user (not joined to any DB row, so audit logs use a sentinel id).
- A weak admin token (e.g. 8 chars) — refused at boot with `error:
  admin-token-too-short` (require ≥32 chars).

---

### Story 10.10 — CSRF protection (web only)

Cookie-based auth (Story 10.2) is vulnerable to CSRF; the bearer-JWT
path is not. This story implements the double-submit-cookie pattern.

**AC-1 — CSRF token issuance.**
- **Given** a successful web login,
- **When** processed,
- **Then** the response carries `mkt_csrf=<32-byte random>` (Story 10.2
  AC-1).

**AC-2 — CSRF token check.**
- **Given** a state-changing request (POST/PUT/PATCH/DELETE) with the
  `mkt_sess` cookie,
- **When** processed,
- **Then** the request must carry `X-Maktaba-CSRF: <token>` whose value
  matches the `mkt_csrf` cookie. Mismatch or missing → 403 `type:
  csrf-failed`.

**AC-3 — Bearer-JWT path skips CSRF.**
- **Given** a request authenticated via `Authorization: Bearer …`,
- **When** processed,
- **Then** CSRF is not enforced (the bearer header itself is the
  proof-of-intent — CSRF can't set custom headers on cross-origin
  requests).

**AC-4 — Safe methods exempt.**
- **Given** GET/HEAD/OPTIONS,
- **When** processed,
- **Then** CSRF is not enforced.

**Test cases:**
- Integration: POST with cookie but no CSRF header → 403.
- Integration: POST with cookie and matching CSRF header → 200.
- Integration: POST with bearer token → 200 regardless of CSRF.
- Integration: a malicious site triggering a form POST cannot include
  the `X-Maktaba-CSRF` header (browsers forbid setting custom headers
  on simple form POSTs).

**Edge cases:**
- CSRF token rotation — the token rotates on each login but persists
  through a session; documented behavior. The SPA reads `mkt_csrf` from
  `document.cookie` once at boot.
- A user who clears cookies mid-session — next request returns 401
  (session unauthenticated), not 403.

---

### Story 10.11 — Brute-force / credential-stuffing protection

Login and refresh endpoints need throttling beyond plain rate-limit
(Story 7.19).

**AC-1 — Per-username lockout.**
- **Given** N failed logins for `username = X` in a window,
- **When** N exceeds `max_failed_logins_per_username` (default 5) within
  `failed_login_window_sec` (default 900 s),
- **Then** subsequent login attempts for that username — *with any
  password* — are rejected with 423 `type: account-locked` until the
  window passes. Successful logins reset the counter.

**AC-2 — Per-IP lockout.**
- **Given** N failed logins from `ip = Y` against any username in a
  window,
- **When** N exceeds `max_failed_logins_per_ip` (default 20) within
  `failed_login_window_sec`,
- **Then** further logins from that IP are throttled with 429 +
  exponentially-increasing `Retry-After`.

**AC-3 — No user enumeration.**
- **Given** a login attempt for an unknown username,
- **When** processed,
- **Then** the timing matches the wrong-password path (Story 10.2 AC-3),
  the response shape is identical (`type: invalid-credentials`), and the
  per-IP counter is incremented (per-username counter is not, since the
  username does not exist).

**AC-4 — Audit on lockout.**
- **Given** a lockout fires,
- **When** the response is returned,
- **Then** an audit row `(action="lockout-username"|"lockout-ip", target,
  count, window)` is written.

**Test cases:**
- Integration: 5 failed logins for `alice` → 6th request 423; valid
  login from a different IP for `alice` is also locked (per-username,
  not per-IP).
- Integration: per-IP lockout from 20 wrong attempts across users.
- Security: timing of unknown-user vs wrong-password is within 50 ms.

**Edge cases:**
- A legitimate user fat-fingers 5 times — locked out for 15 min.
  Document the "wait it out" path and the admin-reset path.
- Distributed credential stuffing across many IPs — per-IP lockout helps
  but isn't enough; the per-username lockout catches it.
- Admin reset endpoint `POST /api/users/{id}/unlock` (admin only)
  immediately clears both counters for the username.

---

### Story 10.12 — Rate limiting on auth endpoints

Coordinated with Story 7.19's general rate-limit middleware. Auth
endpoints get tighter caps because each call is an attack vector.

**AC-1 — Per-IP cap on `/api/auth/*`.**
- **Given** any IP,
- **When** the IP exceeds `auth_rate_per_min` (default 30) on the union
  of `/api/auth/*`,
- **Then** further requests return 429 with `Retry-After`, regardless of
  whether the credentials would have been valid.

**AC-2 — Per-token-family cap on `/refresh`.**
- **Given** a refresh token family,
- **When** more than `refresh_rate_per_min` (default 6) refreshes
  succeed in a minute (a healthy device refreshes every 10 min),
- **Then** further refreshes for that family return 429. Mitigates a
  buggy client spamming refresh.

**Test cases:**
- Integration: 31 logins from one IP in 60 s → 1 of them is 429.
- Integration: a misbehaving client refreshing every 5 s → ratelimited
  after 6 refreshes; access tokens still valid.

**Edge cases:**
- A NAT-shared IP (office) — auth cap of 30/min is generous enough; a
  burst at standup is fine.
- Admin can raise the limits via Story 7.15 settings.

---

### Story 10.13 — Permission model

v1 ships `is_admin` as the only role. Per-resource scope is implemented
via a single `Authz` interface so v2 can add fine-grained roles without
rewriting handlers.

**AC-1 — Resource-scope checks.**
- **Given** a handler,
- **When** it accesses a resource,
- **Then** it calls `authz.Can(ctx, "video.read", video_id)` or
  `authz.Can(ctx, "library.write", library_id)`. Default v1 policy:
  - `*.read` → any authenticated user
  - `*.write` → admin only (or owner-of-the-resource for user-scoped
    resources like `playback_state` and `saved_searches`)
  - `library.*` → admin only

**AC-2 — Per-user scope on `playback_state`.**
- **Given** any user,
- **When** they access `/api/videos/{id}` detail,
- **Then** the response's `playback_state` is filtered to their own
  user_id; users cannot read each other's playback positions.

**AC-3 — Saved searches are per-user.**
- **Given** Story 7.9,
- **When** a non-admin lists or reads,
- **Then** they see only their own.

**AC-4 — Failure mode.**
- **Given** an authorization failure,
- **When** detected,
- **Then** the response is 403 problem+json `type: forbidden` with a
  generic message (don't leak whether the resource exists).

**Test cases:**
- Integration: non-admin POST `/api/libraries` → 403.
- Integration: user A reading user B's playback_state → filtered out.
- Integration: admin reading any resource → allowed.

**Edge cases:**
- A non-admin opens a stream session for a video — allowed (every
  authenticated user can watch); the per-user `playback_state` records
  it under their id. Cross-user resume sync is intra-user only.
- A user is downgraded from admin to viewer mid-session — their
  in-flight access tokens still carry `is_admin: true` until they
  expire (15 min). For instant revocation, force a logout-all (Story
  10.5 AC-3).

---

### Story 10.14 — Secret loading and redaction

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
- **Given** any GET on settings (Story 7.15 AC-1),
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

---

### Story 10.15 — Transport security

TLS, HSTS, secure cookies, CORS, security headers.

**AC-1 — Caddy front by default.**
- **Given** the docker-compose deployment,
- **When** the stack boots,
- **Then** Caddy terminates TLS (auto-issuing via internal CA on `.local`
  hostnames or Let's Encrypt on real domains) and proxies `/api`,
  `/graphql`, `/ws`, `/stream`, `/` to the appropriate backend. The Go
  binaries listen on plain HTTP behind Caddy.

**AC-2 — HSTS.**
- **Given** a TLS-served response,
- **When** the response is built,
- **Then** the header `Strict-Transport-Security: max-age=31536000;
  includeSubDomains` is set on every response. (Configurable; off for
  pure-`.local` setups where the cert isn't browser-trusted.)

**AC-3 — Cookie attributes.**
- **Given** any auth cookie set by Story 10.2,
- **When** inspected,
- **Then** `Secure`, `HttpOnly` (where appropriate), `SameSite=Lax`,
  `Path=/` are set. `Secure` may be unset only when `MAKTABA_DEV=1`
  (logged loudly).

**AC-4 — CORS.**
- **Given** a request from a known origin in `[server].cors_allowed_origins`,
- **When** received,
- **Then** the appropriate `Access-Control-Allow-*` headers are set;
  preflight `OPTIONS` returns 204 with the allow-list of methods and
  headers. Unknown origins → no CORS headers (request fails browser-side).

**AC-5 — Security response headers.**
- **Given** any response,
- **When** built,
- **Then** the API sets:
  - `X-Content-Type-Options: nosniff`,
  - `Referrer-Policy: strict-origin-when-cross-origin`,
  - `Cross-Origin-Opener-Policy: same-origin`,
  - `Content-Security-Policy: <strict baseline>` for the SPA shell.

**Test cases:**
- Integration: a request to `/` returns the CSP and HSTS headers.
- Integration: an unknown CORS origin is silently denied.
- Integration: cookie attributes correct in headers.
- Security scan: `curl --insecure https://maktaba.local/api/system/health`
  on the dev TLS cert succeeds; without `--insecure` only when the
  Caddy local CA cert is trusted.

**Edge cases:**
- A reverse proxy that rewrites `Host` and breaks Caddy's automatic
  cert — operations doc covers `--trust-fc` style flags and known fixes.
- A WebSocket on a non-TLS dev origin — the SPA tries `wss://` first,
  falls back to `ws://` if `MAKTABA_DEV=1`.

---

### Story 10.16 — Audit log for security-sensitive actions

Reuses the `library_audit` infrastructure from Story 9.17 with a
sibling `security_audit` table for auth events.

**AC-1 — Table shape.**
- **Given** `security_audit (id, ts, event, user_id, ip, ua, payload_jsonb)`,
- **When** written,
- **Then** `event` is one of: `login.success`, `login.failed`,
  `logout`, `logout-all`, `lockout-username`, `lockout-ip`,
  `refresh.replay-detected`, `password.changed`, `key.rotated`,
  `admin-token.used` (sampled to avoid noise), `permission.denied`.

**AC-2 — Append-only.**
- Same trigger guard as Story 9.17 AC-1: no UPDATE, no DELETE.

**AC-3 — Surfaced in API.**
- **Given** an admin,
- **When** `GET /api/security/audit?cursor=...` is called,
- **Then** entries are returned newest-first; non-admins receive 403.

**AC-4 — Retention.**
- Same as Story 9.17 AC-3: archive after `audit_retention_days` (default
  365).

**Test cases:**
- Integration: a failed login writes one `login.failed` row; a successful
  login overwrites the username's failure counter and writes
  `login.success`.
- Integration: a non-admin trying to read security audit → 403.

**Edge cases:**
- High-volume admin-token use (single-user mode) — sample at 1/min so the
  audit log doesn't fill with `admin-token.used` rows. The first use per
  IP per day is always logged.
- Audit table partitioned by month for fast retention pruning; document
  in operations.

---

## Cross-cutting & traceability

- **Story-to-architecture map.** Every story header above cites the
  architecture section it implements (§4.x, §5.x, §9.x, §10.x). When
  the architecture changes, search this doc for the cited section to
  find the affected stories.
- **Story-to-test-suite map.** Every AC has at least one test case;
  test cases are written to be 1:1 with `t.Run` / `it()` descriptions
  in the implementation. CI surfaces missing coverage by counting
  AC-tagged tests per story.
- **Feature flag default.** Each story is rolled out behind
  `feature.epic_<n>_story_<m>` in `app_settings`, default `true` once
  the story merges to main. Flags exist for emergency disable, not for
  staged rollout — the system is single-tenant.
- **Sequencing recommendation.** Land in this order: 10.1 → 10.6 →
  7.1 → 7.19 → 7.2 → 9.1 → 7.3/9.16/9.15 → 10.2/10.10/10.15 →
  10.3/10.4/10.5 → 7.4 → 7.18 → 8.1/8.15/10.7/10.8 → 8.3 → 7.10/8.2/8.8/8.9 →
  8.5/8.7/8.10 → 8.4/8.6 → 7.6/8.11/7.7 → 7.8/7.9/9.14 → 9.2/9.3/9.4/9.5/9.6 →
  9.7 → 7.5/7.12/7.13 → 9.8/9.9/9.10/9.11 → 9.12/9.13 → 7.11/7.16 →
  9.17/10.16/10.11/10.12 → 7.14/7.15/10.13/10.14 → 8.12/8.13/8.14 → 7.17 → 7.20.
- **Out of scope across all four epics.** Multi-tenant SaaS, OIDC/SAML
  SSO, 2FA, end-to-end-encrypted libraries, public read-only sharing
  links. All carried in Appendix B of `architecture.md` or the
  follow-up epics.




