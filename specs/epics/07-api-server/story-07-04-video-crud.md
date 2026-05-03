# Story 7.4 — Video list, detail, patch, delete

Endpoints from §9.2: `GET /api/videos`, `GET /api/videos/{id}`,
`PATCH /api/videos/{id}`, `DELETE /api/videos/{id}`.

**AC-1 — List with filters.**
- **Given** a corpus of videos across multiple libraries,
- **When** `GET /api/videos?library={id}&language=ar&type=lecture&tag=tafsir
  &q=foo&sort=updated_at&limit=20` is sent,
- **Then** the response contains only videos matching every filter,
  fewer-than-or-equal-to 20 items, sorted by `updated_at DESC`, with a
  `next` cursor following the §7.2 contract. The query uses covering
  indexes on `videos(detected_language)`, `videos(content_type)`, and
  `video_tags(tag_id, video_id)`.

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
- **When** `?purge=true`, the request must also carry
  `?confirm=<video_id>` matching the path id; otherwise 412
  `type: confirmation-required`. With confirmation, the source file is
  unlinked and an audit row is written to `audit_log (category='library',
  action='video-purge', payload={path}, actor_user_id, ts)`.

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
- Integration: DELETE with `?purge=true&confirm=<id>` against a missing
  file (already deleted out-of-band) returns `204` with a warning header
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
