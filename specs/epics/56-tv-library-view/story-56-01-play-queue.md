# Story 56.01 — A play queue on the server

> Epic 56 · Phase 1 · Depends on: — · Needed by: 56.02, 56.10, 56.11, 56.12, 56.13, Epic 55

## Why

Play all and Shuffle need a queue that can hold a whole library, survive the app closing and
move between devices. Today the only queue is the web's, in one tab's `sessionStorage`, and
Shuffle is one random page of `GET /api/videos` (`sort=random` has no cursor), so a TV cannot
shuffle a library of thousands of videos. Plex keeps play queues on the server; so will Maktaba.

## Description

### 1. Sources and order

`POST /api/me/play-queues` takes `{source: {kind, id, filters?}, shuffle, start_video_id?,
device_id?}`. Kinds and orders are in the README §6.1 (`library`, `series`, `season`,
`collection`, `playlist`, `video`). `filters` for a library are the `FilterKeys` of
`api/internal/handlers/collections/smart.go` (the grid's filters, `sort`, `order`), validated the
same way; any other key is a 422 naming it.

- Not shuffled: the source's own order, read with the same SQL as its list route (one helper per
  kind, shared with those routes so the order never drifts).
- Shuffled: `ORDER BY md5(v.id::text || $seed), v.id`, paged with a cursor on that pair; `seed`
  is a random 63-bit number stored on the queue.
- `series` and `season` start at the next unwatched episode (`next_episode` of
  `GET /api/series/{id}/episodes`, or the season's first unwatched), else the first; the items
  before the start are still in the queue (Previous works).

### 2. Materializing

The queue writes its first chunk of 500 items to `play_queue_items` at creation, with
`source_cursor` pointing past them. When `current_pos` comes within 50 of the materialized end
and `source_done` is false, the next chunk is written in the same transaction as the move.
When more than 5,000 items are held, the oldest played ones are deleted (positions are never
reused). Each chunk filters by `library_acl` for the owner at the time it is written.

### 3. Routes

| Route | Answer |
|---|---|
| `POST /api/me/play-queues` | 201 `{id, source, shuffle, current_pos, version, total_estimate, items: window}`; `window` is the current item with 10 before and 30 after |
| `GET /api/me/play-queues/current` | the user's most recently used queue (any device) or 204 |
| `GET /api/me/play-queues/{id}?around=pos&before=10&after=30` | a window; each item carries the video card fields and `origin` |
| `PUT /api/me/play-queues/{id}/current` `{pos, version}` | 200 with a new version and window; 409 `stale_queue` with the current state when `version` is old |
| `POST /api/me/play-queues/{id}/items` `{video_ids (≤ 200), where: next \| end}` | Play next / Add to queue; `next` goes after the current item, `end` after the last `next`/`added` item and before the next source chunk; renumbering is done by shifting `pos` |
| `POST /api/me/play-queues/{id}/reshuffle` | a new seed; items after the current one are replaced; the current one stays |
| `DELETE /api/me/play-queues/{id}` | 204 |

Every route but `POST` is the owner's only: another user's id answers 404. `POST` answers 404 for
a source the user cannot read (a library outside `library_acl`, another user's playlist), as the
source's own route does. The maintenance loop deletes queues whose `used_at` is older than 7 days.

### 4. Migration (slot 0191, planned; see README §6.2)

`0191_play_queues.sql` and `.sqlite.sql`: the two tables and the index of README §6.2.

## Acceptance criteria

- **Given** a library of 20,000 videos, **when** a viewer creates a shuffled queue and moves
  through 1,200 items, **then** no video repeats, the queue holds at most 5,000 items, and each
  move answers in p95 ≤ 50 ms on the CI Postgres.
- **Given** the same seed, **when** the queue's window is read twice, **then** the order is the
  same; **when** reshuffled, **then** the items after the current one change and the current one
  does not.
- **Given** a series with episodes 1–10 and 1–3 watched, **when** Play is pressed (not shuffled),
  **then** `current_pos` points at episode 4 and Previous reaches episode 3.
- **Given** two devices on one queue, **when** both move with the same `version`, **then** the
  second gets 409 `stale_queue` and the current state.
- **Given** a viewer whose access to a library is removed, **when** the next chunk is written,
  **then** none of that library's videos are in it.
- **Given** no session, or another user's queue id, **then** 401 and 404.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Go unit + Postgres (`internal/testport`) | 2,000 videos | shuffled queue, walk all | each once; chunks of 500; ≤ 5,000 held |
| T02 | Go unit | seed fixed | window twice; reshuffle | same order; tail changes, current kept |
| T03 | Go unit | each source kind | create | order equals the kind's list route |
| T04 | Go unit | series 1–3 watched | create | starts at 4 |
| T05 | Go unit | Play next ×2, Add to queue | window | order current, n1, n2, …, added, next chunk |
| T06 | Go unit | stale version | PUT current | 409 with state |
| T07 | Go role test | no session; viewer; other user | every route | 401; allowed only for own; 404 for others' |
| T08 | Go unit | ACL removed mid-queue | extend | library's videos absent |
| T09 | Go unit | `used_at` 8 days ago | maintenance | deleted |
| T10 | migration test | up/down on Postgres and SQLite | migrate | both twins apply; MANIFEST row |
| T11 | Go benchmark | 150,000 videos | shuffled create + 100 moves | p95 ≤ 50 ms per move |

## Strings

The API returns codes, not text. The apps show these (each app's catalogs, English and Arabic):

| Key | English | Arabic |
|---|---|---|
| `queue.error.stale` | The queue changed on another device. Showing the latest. | تغيّرت قائمة الانتظار على جهاز آخر. يُعرض أحدث إصدار. |
| `queue.error.load` | The queue could not be loaded. | تعذّر تحميل قائمة الانتظار. |
| `queue.empty` | Nothing to play here. | لا يوجد ما يُشغَّل هنا. |

## Edge cases

- **An empty source** (a filter with no match): 422 `empty_source`; the apps show `queue.empty`.
- **A video deleted while queued**: its row cascades away; positions stay, the window skips it.
- **A smart playlist** materializes its filter at creation and on each chunk; videos that stop
  matching later stay in what was already written.
- **SQLite** (the desktop single-user build) has no `md5()`: the store registers a hash
  function with the driver, or orders in Go by the same hash; T01–T05 run on both stores.

## Build plan

| File | Change |
|---|---|
| `shared/db/migrations/0191_play_queues{,.sqlite}.sql`, `MANIFEST.md` | tables |
| `api/internal/handlers/playqueue/` (new): `playqueue.go`, `sources.go`, `chunks.go`, tests | routes, sources, chunks |
| `api/internal/handlers/{series,collections,browse}/` | export each list's ordering helper |
| `api/main.go` (the API's background loops, `loops_test.go`) | delete idle queues every hour |
| `api/internal/router/p6.go` | mount the routes beside the playlists |
| `api/internal/router/browse2_pg_test.go` (or a new `playqueue_pg_test.go`) | T07 against real Postgres |
