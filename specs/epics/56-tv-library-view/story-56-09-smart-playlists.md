# Story 56.09 — Smart playlists

> Epic 56 · Phase 1 · Depends on: — (slot 0192) · Needed by: 56.10, 56.12, 56.13

## Why

Playlists today are manual lists (`user_playlists`, slot 0162). Plex also has smart playlists: a
saved filter with a sort and a limit, that keeps itself up to date, per user. Viewers cannot make
collections (admin-only, README §11 decision 3), so smart playlists are how a viewer keeps
"unwatched lessons of speaker X, newest first" one click away, on every device.

## Description

### 1. Model (slot 0192, planned)

`user_playlists` gains `kind` (`manual` | `smart`) and `smart_query` (README §6.2). A smart query
is `{libraries: [ids] | null (all the user can read), filters: {FilterKeys…}, sort, order,
limit (1–5,000, default 200)}`, validated with `api/internal/handlers/collections/smart.go`'s
`FilterKeys` and the same 422s. `watched` and `favorite` are the owner's own (the playlist is
per user).

### 2. Routes

- `POST /api/me/playlists` and `PATCH /api/me/playlists/{id}` accept `kind` and `smart_query`.
  A smart playlist refuses item writes (`POST …/items`, `DELETE …/items/{id}`, `PUT …/order`:
  409 `smart_playlist`). Changing `kind` is refused (409); make a new one.
- `GET /api/me/playlists/{id}` and its items evaluate the query at read time through
  `GET /api/videos`'s query builder (paged by cursor, capped at `limit`), filtered by
  `library_acl`.
- `GET /api/me/playlists` lists smart ones with `kind`, an approximate `count` (evaluated with a
  5 s cache per playlist) and a poster (the first item's).
- The 200-playlist cap counts both kinds.

### 3. Making one (web; the Players in 56.12)

On the web's library grid and on `/playlists`: **Save as smart playlist** takes the grid's
current filters and sort, asks for a name and a limit, and saves. The playlist page shows the
rules in words ("Unwatched · Speaker: X · Newest first · up to 50"), with **Edit rules** that
reopens the grid with them.

## Acceptance criteria

- **Given** a smart playlist "unwatched, speaker X, newest, 50", **when** one of its videos is
  watched, **then** the next read no longer lists it and the 51st matching video joins.
- **Given** a smart playlist, **when** an item is added through `POST …/items`, **then** 409
  `smart_playlist`.
- **Given** a smart playlist with `libraries = null`, **when** the user loses access to a library,
  **then** none of its videos are listed.
- **Given** another user's playlist id, **then** every route answers 404; no session, 401.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit + Postgres | smart, 60 matching | read; watch one; read | 50; changed membership |
| T02 | Go unit | smart | items write, order, kind change | 409s |
| T03 | Go unit | invalid key, limit 0, limit 6,000 | create | 422 naming the field |
| T04 | Go role test | other user; none | each route | 404; 401 |
| T05 | Go unit | ACL removed | read | library absent |
| T06 | migration test | 0192 up/down, both stores | migrate | check constraint holds |
| T07 | unit (web) | grid with filters | Save as smart playlist | POST body has filters, sort, limit |
| T08 | unit (web) | playlist page en/ar | render rules | words in both languages |
| T09 | e2e (Playwright) | real home | save from grid, open, play | plays the first match |

## Strings

| Key | English | Arabic |
|---|---|---|
| `playlists.smart.save` | Save as smart playlist | احفظ كقائمة تشغيل ذكية |
| `playlists.smart.badge` | Smart | ذكية |
| `playlists.smart.limit` | Up to {n} videos | حتى {n} مقطع |
| `playlists.smart.editRules` | Edit rules | عدّل القواعد |
| `playlists.smart.rules` | Rules | القواعد |
| `playlists.smart.allLibraries` | All libraries | كل المكتبات |
| `playlists.smart.updates` | Updates itself as videos change | تتحدّث تلقائيًا كلما تغيّرت المقاطع |
| `playlists.smart.noEdit` | A smart playlist changes by its rules. Edit the rules instead. | تتغيّر قائمة التشغيل الذكية بحسب قواعدها. عدّل القواعد بدلًا من ذلك. |
| `playlists.smart.empty` | No videos match these rules right now. | لا مقاطع تطابق هذه القواعد الآن. |

## Edge cases

- **A smart query naming a deleted library**: that id is ignored; the rules show "a removed
  library".
- **`sort=random` in a smart query**: refused (422): a playlist's order must be stable; Shuffle
  is the play button.
- **Cloud-linked sign-ins** (play-scoped): may read and play smart playlists, not make them
  (as manual ones today).

## Build plan

| File | Change |
|---|---|
| `shared/db/migrations/0192_smart_playlists{,.sqlite}.sql`, `MANIFEST.md` | columns, check |
| `api/internal/handlers/browse/playlists.go` | kind, query, evaluation, 409s |
| `api/internal/handlers/videos/` | export the query builder for reuse |
| `web/src/pages/Playlists/*`, `web/src/pages/LibraryBrowser.tsx` | save and rules |
| `web/src/i18n/{en,ar}.json` | strings |
