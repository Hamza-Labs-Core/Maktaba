# Story 52.08 — Review API, roles and the audio clip

> Epic 52 · Phase 2 · Depends on: 52.6 · Needed by: 52.9, 52.11, 52.13

## Why

`word_check` (52.6) saves suspect words as `word_flags` rows. Nobody can act on them yet. This
story gives the apps one API to list flags, open one with its context, hear the audio around the
word, and resolve it. The standing rule is that the API enforces every role rule (trail §1), so
the rights in README §4.5 live here, not in the apps.

## Description

### 1. Routes

All routes are under `/api/review`, JSON, and need a signed-in user.

| Method and path | Does |
|---|---|
| `GET /api/review/flags` | Lists flags. Filters: `library`, `status` (default `open`), `language`, `reason`, `video`. Ordered by `priority DESC, id`. Cursor paging (`cursor`, `limit` ≤ 100) |
| `GET /api/review/flags/{id}` | One flag with its context: the sentence before and after, the candidates, the count of other open flags with the same `key` and `language` (`same_key_open`), the video's title, and who holds its lock |
| `GET /api/review/flags/{id}/clip` | Returns `{url, start, end, expires_at}`: a signed URL to the streaming service for the audio around the word |
| `POST /api/review/flags/{id}/resolve` | Body `{action, text?, apply_to_same_key?}`. `action` is `accept`, `correct`, `name`, `dictionary` or `skip`. `text` is required for `correct` and optional for `name` and `dictionary` (a respelling) |
| `POST /api/review/edits/{id}/undo` | Undoes one edit (52.9's rules) |
| `POST /api/review/batches/{id}/undo` | Undoes every edit of one bulk fix |
| `GET /api/review/summary` | Open counts per library, language and reason |

Every route is **home admin only** (§3).

`resolve` with `correct` hands the edit to 52.9's writer in the same transaction. With
`apply_to_same_key: true`, every open flag with the same `key` and `language` gets the same action
as one batch. The response returns `batch_id` and the
number changed.

### 2. The clip

The API never decodes audio. `GET …/clip` returns a short-lived signed URL (5 minutes, the
signed-URL scheme of Epic 10) to the streaming service:

`GET /stream/clip/{video}?track=&start=&end=&sig=`

- The streaming service runs `ffmpeg -ss <start> -t <len> -map 0:a:<track>` on the source file.
  It uses the track the transcript was heard from (`word_flags.audio_track_id`, PR #556's chosen
  stream).
- The window is the word's `start_sec − 1.5 s` to `end_sec + 1.5 s`, capped at 12 s. When the
  flag's time is estimated (`time_estimated`), the clip is the whole sentence.
- The output is AAC in fragmented MP4. Opus in WebM is served when the client asks with
  `Accept: audio/webm`. Mono, 64 kb/s.
- Clips are cached on disk under the streaming cache by `(video, track, start, end, format)`.
  The cache keeps 500 MB and evicts the least recently used. A cache hit returns in < 50 ms.

### 3. Roles (enforced here): home admins only

Decision 1 (README §8, 2026-11-03): **only home admins review**. The check
is inline, the way `transcription.go:304` does it:

- `p == nil` → 401.
- `p.IsAdmin` → allowed on every library.
- Anything else → 403, **whatever the user's `library_acl.role`**: `write` and the library
  `admin` role grant nothing here. `canReview` does not read `library_acl` at all.
- Cloud-linked, play-scoped sign-ins → 403, even with manage granted, unless the account is
  itself a home admin.
- Undo: any home admin may undo any edit.

### 4. Soft lock

Opening a flag (`GET /flags/{id}`) takes a 5-minute soft lock (`locked_by`, `locked_until` on
`word_flags`, added in this story's migration). Another admin still sees the flag, marked "Being
reviewed by <name>". The list skips locked flags by default. A resolve by someone else while it is
locked returns 409 with the holder's name, unless the caller is an admin and sends `force: true`.
The lock is released on resolve, on skip, or when it expires.

## Acceptance criteria

1. **Given** a home admin, **when** they call `GET /api/review/flags` with no filter, **then** open
   flags of every library come back, ordered by priority.
2. **Given** a user with `write` on library A (not a home admin), **when** they list, open,
   resolve or clip a flag of library A, or undo an edit there, **then** the API answers 403 and
   nothing changes. The same holds for a user with the library `admin` role on A.
3. **Given** a viewer (`read` on every library), **when** they call any `/api/review` route,
   **then** the answer is 403. **Given** no session, **then** 401.
4. **Given** an open flag, **when** an admin resolves it with `correct` and `text`, **then** the
   flag is `corrected`, `resolution` holds the text, a `transcript_edits` row exists (52.9), and
   the response returns the edit id.
5. **Given** a flag with `same_key_open = 37`, **when** the admin resolves it with
   `apply_to_same_key: true`, **then** all 38 flags change in one batch, the response says 38, and
   one batch undo reverses all of them.
6. **Given** a flag, **when** its clip URL is fetched within 5 minutes, **then** audio from the
   transcript's own track plays, covering the word with 1.5 s either side. **When** it is fetched
   after the URL expires, **then** the answer is 403.
7. **Given** admin X has a flag open, **when** admin Y resolves it without `force`, **then** Y gets
   409 with X's name. **When** the lock expires after 5 minutes, **then** Y can resolve it.
8. **Given** `resolve` with `correct` and empty `text`, or an unknown action, **then** the answer
   is 400 with a reason code, and nothing changes.
9. **Given** a flag whose sentence changed since it was made (status `stale`), **when** someone
   resolves it with `correct`, **then** the answer is 409 `stale` and they are told to reopen it.
10. **Given** an admin, **when** they call `GET /api/review/summary`, **then** they see the counts
    for every library. Any non-admin gets 403.
11. **Given** a flag with reason `quran_mismatch`, **when** an admin opens it, **then** the response
    carries the verse reference and expected word from `scores.quran` (52.15).

## Build plan

### Files

- `api/internal/handlers/review/review.go`: routes, handlers, role checks.
- `api/internal/handlers/review/store.go`: SQL (pgx and the SQLite path), cursor paging.
- `api/internal/handlers/review/access.go`: `canReview(ctx, p)`: `p != nil && p.IsAdmin`, never
  `library_acl`. Shared with 52.9, 52.10, 52.12 and 52.14.
- `api/internal/router/p6.go`: mount `review.Routes`. `api/internal/router/placement.go`: list
  the routes as home-only (a node never serves them).
- `streaming/internal/handlers/clip.go`: the clip endpoint, signature check, ffmpeg, cache.
- `shared/db/migrations/0182+_word_flags_lock.sql` and `.sqlite.sql`: `locked_by UUID NULL`,
  `locked_until TIMESTAMPTZ NULL` on `word_flags`.
- `web/src/lib/reviewApi.ts`: typed client (used by 52.11), with reason and status codes as one
  union that a test checks against the Go constants.

### Tests

- `review_test.go` `TestReviewRoles` in the `TestAdminOnly` style
  (`handlers/cluster/cluster_test.go:629`). It loops over every route as: nobody (401), viewer
  (403), `write` on this library (403), library `admin` role on this library but not a home admin
  (403), cloud-linked play-scoped with manage granted (403), home admin (200). A guard test fails
  if `canReview` ever reads `library_acl`.
- `review_pg_test.go` on a scratch Postgres, and the same on SQLite. It covers list filters and
  ordering, cursor stability under inserts, the same-key batch, the 409 lock, the stale 409, and
  the summary per caller.
- `clip_test.go`: a two-track ffmpeg-made file (the PR #556 fixture style: 440 Hz track 0,
  1000 Hz track 1). The clip of a track-1 flag decodes to 1000 Hz, its length is within 50 ms of
  the asked window, an expired signature gets 403, a cache hit does not run ffmpeg.
- Contract test: the reason and status codes in Go, Python (`word_flags` writer) and
  `reviewApi.ts` are the same list.

### Rollout

Ships with phase 2. The routes are useless until 52.11, so they go live with it in one release.
The clip cache size is a streaming setting with a 500 MB default.

## Edge cases

- **The video was deleted or its file is missing:** the clip returns 404 `media_missing` and the
  app still shows the sentence. Flags of a deleted video go with it (`ON DELETE CASCADE` through
  `transcripts`).
- **The file was replaced (new content hash):** its transcript is replaced too. Old flags go stale
  or are carried over (52.9).
- **The word is near the file's start or end:** the window is clamped to `[0, duration]`.
- **An admin loses admin rights while holding a lock:** the next call is 403. The lock expires by
  itself.
- **Bulk fix across libraries:** an admin's bulk fix covers every library's open flags of that key
  and language.
