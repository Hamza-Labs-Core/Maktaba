# Story 52.08 — Review API, roles and the audio clip

> Epic 52 · Phase 2 · Depends on: 52.6 · Needed by: 52.9, 52.11, 52.13

## Why

`word_check` (52.6) saves suspect words as `word_flags` rows. Nobody can act on them yet. This
story gives the apps one API to list flags, open one with its context, hear the audio around the
word, and resolve it. The owner's rule is that the API enforces every role rule (trail §1), so
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
| `GET /api/review/summary` | Open counts per library, language and reason, for the caller's libraries only |

`resolve` with `correct` hands the edit to 52.9's writer in the same transaction. With
`apply_to_same_key: true`, every open flag with the same `key` and `language` in libraries the
caller may review gets the same action as one batch. The response returns `batch_id` and the
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

### 3. Roles (enforced here)

The check is inline, the way `transcription.go:304` does it, plus the per-library role:

- `p == nil` → 401.
- `p.IsAdmin` → allowed on every library.
- Otherwise, the flag's `library_id` must have a `library_acl` row for the user with `role IN
  ('write','admin')` → allowed. Anything else → 403.
- Cloud-linked, play-scoped sign-ins → 403, unless a home admin granted manage.
- `GET /flags` and `/summary` never return a row from a library the caller may not review. The
  filter is in the SQL, not after it.
- Undo: admins may undo anyone's edit. A reviewer may undo only edits where `user_id` is theirs.

### 4. Soft lock

Opening a flag (`GET /flags/{id}`) takes a 5-minute soft lock (`locked_by`, `locked_until` on
`word_flags`, added in this story's migration). Another reviewer still sees the flag, marked "Being
reviewed by <name>". The list skips locked flags by default. A resolve by someone else while it is
locked returns 409 with the holder's name, unless the caller is an admin and sends `force: true`.
The lock is released on resolve, on skip, or when it expires.

## Acceptance criteria

1. **Given** a reviewer with `write` on library A only, **when** they call `GET /api/review/flags`
   with no filter, **then** only library A's open flags come back, ordered by priority.
2. **Given** the same reviewer, **when** they open, resolve or clip a flag in library B, **then**
   the API answers 403 and nothing changes.
3. **Given** a viewer (`read` on every library), **when** they call any `/api/review` route,
   **then** the answer is 403. **Given** no session, **then** 401.
4. **Given** an open flag, **when** a reviewer resolves it with `correct` and `text`, **then** the
   flag is `corrected`, `resolution` holds the text, a `transcript_edits` row exists (52.9), and
   the response returns the edit id.
5. **Given** a flag with `same_key_open = 37`, **when** the reviewer resolves it with
   `apply_to_same_key: true`, **then** all 38 flags they may review change in one batch, the
   response says 38, and one batch undo reverses all of them.
6. **Given** a flag, **when** its clip URL is fetched within 5 minutes, **then** audio from the
   transcript's own track plays, covering the word with 1.5 s either side. **When** it is fetched
   after the URL expires, **then** the answer is 403.
7. **Given** reviewer X has a flag open, **when** reviewer Y resolves it, **then** Y gets 409 with
   X's name. **When** the lock expires after 5 minutes, **then** Y can resolve it.
8. **Given** `resolve` with `correct` and empty `text`, or an unknown action, **then** the answer
   is 400 with a reason code, and nothing changes.
9. **Given** a flag whose sentence changed since it was made (status `stale`), **when** someone
   resolves it with `correct`, **then** the answer is 409 `stale` and they are told to reopen it.
10. **Given** an admin, **when** they call `GET /api/review/summary`, **then** they see the counts
    for every library. A reviewer sees only their libraries.

## Build plan

### Files

- `api/internal/handlers/review/review.go`: routes, handlers, role checks.
- `api/internal/handlers/review/store.go`: SQL (pgx and the SQLite path), cursor paging.
- `api/internal/handlers/review/access.go`: `canReview(ctx, p, libraryID)` from `principal` and
  `library_acl`. Shared with 52.9, 52.10, 52.12 and 52.14.
- `api/internal/router/p6.go`: mount `review.Routes`. `api/internal/router/placement.go`: list
  the routes as home-only (a node never serves them).
- `streaming/internal/handlers/clip.go`: the clip endpoint, signature check, ffmpeg, cache.
- `shared/db/migrations/0173+_word_flags_lock.sql` and `.sqlite.sql`: `locked_by UUID NULL`,
  `locked_until TIMESTAMPTZ NULL` on `word_flags`.
- `web/src/lib/reviewApi.ts`: typed client (used by 52.11), with reason and status codes as one
  union that a test checks against the Go constants.

### Tests

- `review_test.go` `TestReviewRoles` in the `TestAdminOnly` style
  (`handlers/cluster/cluster_test.go:629`). It loops over every route as: nobody (401), viewer
  (403), reviewer of another library (403), cloud-linked play-scoped (403), reviewer of this
  library (200), admin (200).
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
- **A reviewer loses `write` while holding a lock:** the next call is 403. The lock expires by
  itself.
- **Bulk fix across libraries:** only flags in libraries the caller may review change. The response
  says how many were left out.
