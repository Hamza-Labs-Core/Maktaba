# Story 56.06 — Automatic collections from YouTube playlists and channels

> Epic 56 · Phase 2 · Depends on: — (slot 0192 shared with 56.07, 56.08) · Needed by: 56.08

## Why

Most of a Maktaba library came from YouTube, where its creators already grouped it into
playlists and channels. Today the pipeline turns a playlist into a **season** when it matches
one, and into a **topic collection** (`topic_collections`, slot 0094) only when it cuts across
shows (`find_topics` in `classify/series_detector.py`). Every other playlist, and every channel,
is lost to browsing. Plex builds collections automatically from its metadata; Maktaba's
metadata is these playlists and channels.

## Description

### 1. Which playlists and channels

A new library-level stage, `youtube_collections`, runs after `topic_collections` (and on the
6-hour reconciler, like `youtube_derive.py`). For each library:

- **Playlist collections**: every `youtube_playlists` row with `is_real = true` that has at least
  **3** of the library's videos (videos join through `media_files.external_id = 'youtube:<id>'`
  and `youtube_playlist_items`) becomes a collection with `source_kind = 'youtube_playlist'`,
  `source_ref = playlist_id`, name = the playlist's title, members in the playlist's `position`
  order — **unless** its members are the same set as one season (the season stands in) or it is
  already a topic collection's `playlist_id` (the topic collection stands in, and gets the
  YouTube badge through its `playlist_id`).
- **Channel collections**: when a library holds videos of **2 or more** channels
  (`youtube_channel_libraries`, slot 0182), each channel with at least 3 of its videos becomes a
  collection with `source_kind = 'youtube_channel'`, `source_ref = channel_id`, members newest
  published first. A library of one channel gets none (it would be the whole library).

### 2. Keeping them in step

Each run computes a `membership_hash`; an unchanged hash writes nothing. A changed one replaces
`collection_items` for that collection in one transaction. A playlist that falls below the
threshold or disappears deletes its collection (and its promotion flags with it). An admin's
`name_override`, `hidden_at`, `show_on_*` and `sort_title` are kept across runs (README §11,
decision 4).

Writes go through the queue engine's usual change records (`queue.change.created.collection`
and friends already exist), so the library board shows them.

### 3. Turning it off

The stage is a component part under Epic 51's catalog (`collections.youtube`, default on); off,
it deletes nothing and writes nothing.

## Acceptance criteria

- **Given** a library with a real playlist of 12 of its videos that is neither a season nor a
  topic, **when** the stage runs, **then** a collection named after the playlist exists in that
  library with the 12 in playlist order.
- **Given** a playlist whose members are exactly one season, **then** no collection is made.
- **Given** a library of videos from 3 channels, **then** 3 channel collections exist; a library
  of one channel has none.
- **Given** an admin renamed and hid a playlist collection, **when** the playlist gains a video,
  **then** the collection gains it and keeps its name and hidden state.
- **Given** the stage runs twice with no change, **then** the second run writes no rows.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | pytest (pipeline, real Postgres) | playlist 12, season, topic fixtures | run | one playlist collection; none for season or topic |
| T02 | pytest | 3 channels; 1 channel | run | 3; 0 |
| T03 | pytest | admin overrides | membership change | items changed, overrides kept |
| T04 | pytest | unchanged | run twice | 0 writes the second time |
| T05 | pytest | playlist drops to 2 | run | collection deleted |
| T06 | pytest | component off | run | no writes |
| T07 | Go unit | `GET /api/libraries/{id}/collections?kind=youtube` | list | `source_kind` and counts |
| T08 | migration test | slot 0192 up/down, Postgres and SQLite | migrate | columns and unique index |

Run with `uv run --extra dev pytest`; mypy strict covers the tests.

## Strings

| Key | English | Arabic |
|---|---|---|
| `collections.badge.youtubePlaylist` | YouTube playlist | قائمة تشغيل يوتيوب |
| `collections.badge.youtubeChannel` | YouTube channel | قناة يوتيوب |
| `collections.kind.youtube` | From YouTube | من يوتيوب |
| `queue.stage.youtube_collections.title` | Collections from YouTube | مجموعات من يوتيوب |
| `queue.stage.youtube_collections.desc` | Makes a collection from each YouTube playlist and channel a library's videos came from. | ينشئ مجموعة من كل قائمة تشغيل وقناة يوتيوب جاءت منها مقاطع المكتبة. |
| `components.collections.youtube.title` | Collections from YouTube | مجموعات من يوتيوب |

## Edge cases

- **A playlist shared by two libraries**: a collection in each, each with its own library's
  videos (thresholds per library).
- **A playlist renamed on YouTube**: the name follows unless the admin set `name_override`.
- **A private or deleted playlist**: `youtube_playlists` keeps its last facts; the collection
  stays until the facts are removed.

## Build plan

| File | Change |
|---|---|
| `shared/db/migrations/0192_collection_sources{,.sqlite}.sql`, `MANIFEST.md` | README §6.2 |
| `pipeline/src/maktaba_pipeline/engine/stages/youtube_collections.py` (new), `registry.py` | the stage |
| `pipeline/src/maktaba_pipeline/engine/reconcile.py` | 6-hour pass |
| `api/internal/handlers/browse/groups.go` | `kind=youtube`, `source_kind` |
| component catalog entry (Epic 51) | `collections.youtube` |
| `web/src/i18n/{en,ar}.json`, Apple and Android catalogs | strings |
