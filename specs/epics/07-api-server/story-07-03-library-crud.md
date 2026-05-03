# Story 7.3 — Library CRUD

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
- **When** `?purge=true` is sent, the request **must** also carry
  `?confirm=<library_name>` matching the library's `name`; otherwise the
  request is rejected with 412 `type: confirmation-required`. With
  confirmation, the on-disk files are deleted from each root **after**
  the DB rows are removed and the response only returns `204 No Content`
  once all deletions complete.

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
  joining `videos` + `processing_jobs`. (Full composition lives in
  Epic 9 Story 9.7.)

**Test cases:**
- Unit: deep-merge of `settings` preserves nested keys not mentioned in the
  patch.
- Integration: create library with two roots, one absolute and one relative
  → 422 listing the relative one.
- Integration: rename uniqueness — POST a second library with the same
  `name` returns 409 `type: library-name-exists`.
- Integration: `DELETE ?purge=true` without confirmation → 412; with
  matching `?confirm=<name>` against a library whose root is a
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
