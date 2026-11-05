# Epic 56: The TV library view: Play all, Shuffle, series, collections and playlists

> **Status:** spec (2026-11-05). **Builds on:** [Epic 7](../07-api-server/README.md)
> (collections, slot 0033), [Epic 26](../26-content-intelligence/README.md) (series detection,
> auto-collections, slot 0076), [Epic 45](../45-libraries-ui/README.md) (library tabs, the grid's
> filters, `sort=random`, saved filters), [Epic 46](../46-big-screen/README.md) (the web's 10-foot
> view), [Epic 48](../48-native-players/README.md) (the native Players and the TV apps on the shared
> core), the Plex stages (`pipeline/src/maktaba_pipeline/extensions/plex/`) and the YouTube
> sources (slots 0166, 0175, 0182). **Works with:** Epic 55 (the desktop Player's parity, written
> in parallel), which takes the play queue of story 56.01 as it is. **Research:**
> [research.md](research.md). **Wiki:**
> [epic-56-tv-library-view.md](../../../docs/wiki/epics/epic-56-tv-library-view.md).

## 1. Why

Requirement: on the TV apps (Android TV and Apple TV), a library must offer **Play all** and
**Shuffle**, and must be browsable **by series, by collection and by playlist**, as Plex's TV
apps are. Today a library on a TV is one grid of videos, newest first, with no sort, no filter
and no way to play more than one video.

The server already has most of what this needs (series and seasons, admin and automatic
collections, user playlists, a random sort), and the web and the phone Players already show
some of it. The TV apps were built last, on the shared core (Epic 48), and stopped at the grid.

## 2. What exists, and the gaps

Checked on `main` at 357851f7d (2026-11-05).

### 2.1 The server

| Area | Today | Where |
|---|---|---|
| Series and episodes | `series`, `series_episodes` (season, episode, absolute number) | slot 0075 |
| Seasons | `series_seasons` with a `kind` (episodic, ongoing, short-form, clips) | slot 0088 |
| Admin collections | `collections` (`is_smart`, `smart_query`), `collection_items` (position) | slot 0033 |
| Automatic collections | `collections.origin`, `auto_rule`, `collection_suggestions`; the pipeline's `topic_collections` and `clip_groups` | slots 0076, 0094 |
| Playlists | `user_playlists`, `user_playlist_items` (position), `user_favorites`; owner only, 200 lists, 5,000 items | slot 0162 |
| Progress | `playback_state` (position, completed), Continue Watching index, `watch_sessions` | slots 0038, 0066, 0086 |
| YouTube | `youtube_playlists`, `youtube_playlist_items`, `youtube_channels`, `external_links`; `youtube_sources`, `youtube_channel_libraries` | slots 0090, 0166, 0182 |
| Plex | `plex_items` (Plex rating key ↔ Maktaba video); Maktaba **writes** season and topic collections to Plex; it never reads Plex's collections or playlists | slot 0092, `extensions/plex/{index,collection,groups}.py` |
| Routes | `GET /api/libraries/{id}/hub`, `/series`, `/collections?kind=`, `/collections/{kind}/{gid}`; `GET /api/series`, `/{id}`, `/{id}/episodes` (with `next_episode`); `/api/collections` CRUD (writes admin only); `/api/me/playlists` CRUD and order; `GET /api/videos` with filters, `sort=random` (one page, no cursor) | `api/internal/handlers/{browse,series,collections,videos}` |
| Play queue | **none on the server**: the web keeps its queue in the tab's `sessionStorage` (`web/src/lib/playQueue.ts`) | |
| OpenAPI | `shared/api/openapi.json` has `/libraries` and `/collections` only, and names the items path `/collections/{id}/videos` while the code serves `/items`; series, seasons, playlists, hub and `/me/*` are missing | |

### 2.2 The apps

| Feature | Web | Web `/big` | Android Player | Apple Player | Android TV | Apple TV |
|---|---|---|---|---|---|---|
| Library tabs | Home, Browse, Series, Collections | hub + grid | Videos, Series | Videos, Series | none | none |
| Sort and filter | yes (facets, letter) | no | sort + facets | sort + facets | no | no |
| Series page, season picker, Continue | yes | yes | yes | yes | no | no (MaktabaKit's `SeriesPage` is Player only) |
| Collections (browse, play) | yes | no | no | no | no | no |
| Playlists | yes (own page, add to) | no | Profile only | Profile only | no | no |
| Play all / Shuffle | library grid, collection, playlist | no | no | no | no | no |
| Play queue | `sessionStorage` | queue advance only | no | no | no | no |
| Up next after a video | queue, else next lesson | next lesson | next episode | next episode | no | no |
| Home | `/api/home` | `/api/home` | `/api/home` | `/api/home` | `/api/recommendations` only | `HomeShelves` (`/api/home`) |

Android TV: `apps/tv/android/app/src/main/java/com/hamzalabs/maktaba/tv/ui/screens/LibrariesScreen.kt`
(`LibraryScreen` is one paged grid from `GET /api/videos`). Apple TV:
`apps/tv/tvos/MaktabaTV/Views/LibraryView.swift` (a `VideoGrid`), `VideoPage.swift` (Resume,
Play from the start, Play).

### 2.3 Gap list

Against the Players and the web (G) and against Plex (P):

- **G1** No Play all or Shuffle on any TV surface, and none on the phone Players.
- **G2** No server play queue: a queue lives in one browser tab, cannot be continued on another
  device, and Shuffle is one random page (`sort=random` has no cursor), so a TV cannot shuffle a
  whole library of thousands of videos.
- **G3** No series view on the TVs: no Series tab, series page, season page or Continue.
- **G4** No collections on the TVs or the phone Players.
- **G5** No playlists on the TVs; on the phone Players only under Profile, and nowhere a list can
  be played from a library.
- **G6** No sort, filter or letter jump on the TVs.
- **G7** No Up next on the TVs: a video ends and the player stops.
- **G8** Android TV's Home ignores `/api/home` (no Continue Watching, On Deck).
- **G9** OpenAPI does not describe series, seasons, playlists, hubs or `/me/*`.
- **P1** Plex has five tabs per library on its TV apps (Recommended, Library, Collections,
  Playlists, Categories); Maktaba's TVs have none.
- **P2** Plex creates collections automatically from metadata; Maktaba has topic and clip groups
  but none from YouTube playlists or channels, and reads none of Plex's own collections.
- **P3** Plex has smart playlists (saved filters, per user); Maktaba's playlists are manual only.
- **P4** Plex lets an admin choose per library whether collections show inline in the grid and
  whether their items hide (collection mode); Maktaba has no such choice.
- **P5** Plex can show a collection as a row on the library's Recommended tab and on Home;
  Maktaba cannot.
- **P6** Plex keeps play queues on the server with Play next and Add to queue; Maktaba has no
  server queue (G2).

## 3. Goals

1. **Play all and Shuffle** on a library (in the view's sort and filter), a series, a season, a
   collection and a playlist, on both TV apps, the phone Players and the web, all through one
   server play queue.
2. **Five library tabs on the TVs**, as Plex: Recommended, Library, Collections, Playlists,
   Categories, each reachable with the remote alone.
3. **Series grouping**: the Library tab shows videos or series; a series page and season pages
   with Continue (the next unwatched episode), Play and Shuffle.
4. **Collections**: automatic ones from YouTube playlists, YouTube channels and Plex's collections
   (besides today's topic and clip groups), and admin-made ones (manual and smart, as today);
   each can be a Recommended row.
5. **Playlists**: the user's own manual and smart playlists, played and added to from the TV.
6. **Continue and Up next** on the TVs: On Deck and Continue Watching rows, and the queue's next
   video after each one, with a countdown that can be cancelled.
7. **The same features in the Players and the web** where they fit (§9).
8. Every new string in English and Arabic; every layout mirrored in Arabic, including the
   D-pad's left and right.

## 4. Non-goals

- **No user-made collections.** Collections stay home-wide and written by admins (the API
  already refuses viewers); viewers have playlists. This is Plex's split too.
- **No shared playlists** between users in this epic (Plex lets an admin share one with managed
  users; a later epic may).
- **No Plex playlists import.** Plex's playlists belong to Plex users who need not exist in
  Maktaba; only Plex's collections, which are per library, are imported (56.07).
- **No writing back to Plex** beyond what the Plex stages already do.
- **No Watchlist or discovery lists** (Plex's Lists and Discover are cloud features over a
  catalogue Maktaba does not have).
- **No smart shuffle** (Plex's music-only weighting toward rated, popular, unheard tracks):
  Shuffle is uniformly random, with an Unwatched filter for "only what I have not seen".
- **No skip intro, extras or trailers.**
- **No desktop Player work here**: Epic 55 covers the desktop Player and uses 56.01's queue.

## 5. Plex and Maktaba, compared

Sources in [research.md](research.md).

| Plex (TV apps) | Maktaba (this epic) | Why |
|---|---|---|
| Library tabs: Recommended, Library, Collections, Playlists, Categories; opens on Recommended | Same five tabs and names; opens on **Recommended**, the last tab chosen is kept per library on the device | Match: the layout people know |
| Recommended: hubs Plex builds (On Deck, Recently Added, by genre…) plus collections promoted to rows | The library's hub (On Deck, Continue Watching, Recently Added, Recently Watched) plus collections an admin promotes (56.08) | Match, with Maktaba's own hubs |
| Library tab: a type picker (Movies / Shows / Seasons / Episodes), sort, filters, a letter jump bar | A **Show** picker (Videos / Series), sort, filters (the grid's facets), a letter jump bar that knows Arabic letters | Match; lectures are videos or series, not movies or shows |
| Categories: genre, year, decade, director, actor… | **Speakers, Topics, Languages, Years** (the grid's facets) | Differ: Maktaba's metadata is speakers and topics, not cast |
| Collections: admin-made, manual or smart; automatic from metadata (TMDB) | Admin-made manual or smart (as today); automatic from **YouTube playlists, YouTube channels, Plex collections** and today's topic and clip groups | Match on the split; Maktaba's automatic sources are its own |
| Collection mode per library: hide collections, hide their items, show both | Same three, per library, set by an admin; default **collections only in their tab** | Match |
| A collection can be a row on Recommended and Home | Same two flags per collection | Match |
| Playlists: per user, manual or smart (a saved filter with a limit and sort) | Same; smart ones re-evaluate on open and on play | Match |
| Play on a show plays the On Deck episode on; Shuffle shuffles every episode | Play on a series or season starts at its **next unwatched** episode (the first when all are watched) and goes on in episode order; Shuffle takes every episode | Match |
| Play on a library plays the current view in order; Shuffle shuffles it | Same: the view's sort and filter decide what plays | Match |
| Server play queues with a window around the current item; Play next, Add to queue | Server play queue (56.01) with a window, Play next and Add to queue; kept 7 days after its last use | Match |
| Auto-play the next episode with a countdown | Up next card with a 10 s countdown (the Players' setting), from the queue | Match |
| Smart shuffle for music | Plain shuffle | Differ (non-goal) |
| Left-to-right everywhere | Arabic mirrors the layout and the D-pad's left and right | Differ: Maktaba is bilingual |

## 6. Design

### 6.1 One play queue on the server (56.01)

Every Play and Shuffle creates a **play queue** from a **source**:

| `source.kind` | `source.id` | Order when not shuffled | Start |
|---|---|---|---|
| `library` | library id, plus `filters` (the `GET /api/videos` keys of `FilterKeys`, including `sort`, `order`) | the view's sort | the first item, or `start_video_id` |
| `series` | series id | season, then episode (`absolute_number` when the series numbers that way) | next unwatched, else the first |
| `season` | `series_seasons.id` | episode | next unwatched, else the first |
| `collection` | collection id, or `topic:<gid>` / `clip:<gid>` for the pipeline's groups | the collection's position | the first |
| `playlist` | playlist id (the caller's own) | the playlist's position; a smart one, its saved sort | the first |
| `video` | video id | that video, then (when it is an episode) the rest of its series | the video |

- **Shuffle** orders the source by a seeded hash (`md5(video_id || seed)`), so it is uniform,
  pages with a cursor, and never holds the whole library in memory. The seed is stored, so
  "Shuffle again" makes a new seed and the same queue reloads the same order.
- The queue **materializes items in chunks of 500** (`play_queue_items`), extending from the
  source's cursor as the current item nears the end; a queue never holds more than 5,000 items
  at once (older played ones drop off the front).
- **Play next** inserts after the current item; **Add to queue** appends after the source's
  materialized end and before the next chunk.
- A queue belongs to one user and records the device that made it; `GET /api/me/play-queues/current`
  returns the user's latest, so a queue started on a phone can be continued on a TV.
- Queues not used for 7 days are deleted by the API's maintenance loop.
- Access: every item is filtered by `library_acl` when it is materialized, not only when the
  queue is made; a video whose library the user loses is skipped on the next window read.

### 6.2 Data model and migrations

Slots **0184, 0185 and 0186 are planned for this epic** (main's highest is 0183 on 2026-11-05).
Per `shared/db/migrations/MANIFEST.md`, a slot is taken when its migration merges: if main has
moved past one when a story lands (Epic 52's plan also names 0184 as its next free slot), that
story takes the next free integer and updates this table. Each has a `.sqlite.sql` twin.

| Slot | Story | File | Contents |
|---|---|---|---|
| 0184 | 56.01 | `0184_play_queues.sql` | `play_queues` (id uuid pk, user_id fk, device_id text null, source_kind text check, source_id text, source_filters jsonb, shuffle bool, seed bigint null, source_cursor text null, source_done bool, current_pos int, version int, created_at, used_at); `play_queue_items` (queue_id fk cascade, pos int, video_id fk cascade, origin text check `source`/`next`/`added`, pk (queue_id, pos)); index `(user_id, used_at desc)` |
| 0185 | 56.09 | `0185_smart_playlists.sql` | `user_playlists` ADD `kind text not null default 'manual'` check `manual`/`smart`, `smart_query jsonb null` (the `SmartFilter.Filters` keys plus `libraries` (array), `limit` ≤ 5,000), check (kind = 'smart') = (smart_query is not null); smart playlists hold no `user_playlist_items` |
| 0186 | 56.06, 56.07, 56.08 | `0186_collection_sources.sql` | `collections` ADD `source_kind text null` check `youtube_playlist`/`youtube_channel`/`plex`, `source_ref text null`, `name_override text null`, `hidden_at timestamptz null`, `show_on_library bool not null default false`, `show_on_home bool not null default false`, `sort_title text null`; unique `(library_id, source_kind, source_ref)` where `source_kind` is not null; `libraries` ADD `collection_mode text not null default 'tab_only'` check `tab_only`/`hide_items`/`show_both` |

### 6.3 API

New routes (all under the signed-in user; the API enforces each role rule, with a role test
that tries every route as no one and as a viewer):

| Method and path | Who | Story |
|---|---|---|
| `POST /api/me/play-queues` `{source:{kind,id,filters?}, shuffle, start_video_id?, device_id?}` → the queue with a window | any signed-in user, for sources they can read | 56.01 |
| `GET /api/me/play-queues/current`, `GET /api/me/play-queues/{id}?around=&before=&after=` | the owner | 56.01 |
| `PUT /api/me/play-queues/{id}/current` `{pos, version}` (409 on a stale version) | the owner | 56.01 |
| `POST /api/me/play-queues/{id}/items` `{video_ids, where: next\|end}` | the owner | 56.01 |
| `POST /api/me/play-queues/{id}/reshuffle`, `DELETE /api/me/play-queues/{id}` | the owner | 56.01 |
| `GET /api/libraries/{id}/categories` → speakers, topics, languages, years with counts | readers of the library | 56.05 |
| `GET /api/libraries/{id}/collections` gains `kind=youtube\|plex` and returns `source_kind`, `show_on_library` | readers | 56.06–56.08 |
| `PATCH /api/collections/{id}` accepts `name_override`, `hidden`, `show_on_library`, `show_on_home`, `sort_title` (automatic ones: only these) | admin | 56.08 |
| `PATCH /api/libraries/{id}` accepts `collection_mode` | admin | 56.08 |
| `GET /api/libraries/{id}/hub` gains promoted collection rows; `GET /api/home` gains `show_on_home` rows | readers | 56.08 |
| `GET /api/me/playlists?library=` (lists with an item from that library; smart ones whose filter covers it) | the owner | 56.10 |
| `POST /api/me/playlists` and `PATCH` accept `kind`, `smart_query`; `GET /api/me/playlists/{id}/items` evaluates a smart list | the owner | 56.09 |
| `GET /api/videos` with `group=series` returns series cards in the grid's sort and filter | readers | 56.04 |

OpenAPI describes every route above and the existing ones the TVs read (series, seasons,
playlists, hub, home, facets, `/me/*`), and fixes the collections items path (56.14).

### 6.4 The TV screens

**A library** (both TVs):

```
┌──────────────────────────────────────────────────────────────────────┐
│  Library name                       [▶ Play]  [⤮ Shuffle]            │
│  Recommended   Library   Collections   Playlists   Categories        │
├──────────────────────────────────────────────────────────────────────┤
│  Recommended: rows (On Deck, Continue Watching, Recently Added,      │
│  promoted collections…)                                              │
│  Library: Show [Videos|Series]  Sort [Newest ▾]  Filter [2]  A…Z ي…ا │
│           grid of cards                                              │
│  Collections: grid of collection cards (badge: YouTube, Plex, Smart) │
│  Playlists: the user's lists with items here, + New smart playlist   │
│  Categories: Speakers · Topics · Languages · Years → filtered grid   │
└──────────────────────────────────────────────────────────────────────┘
```

Play and Shuffle sit in the header on every tab: on Recommended and Library they play the
Library tab's current view (its sort and filter); on Collections and Playlists they are not shown
in the header (each card's page has its own).

**A series**: backdrop, name, counts, description; buttons **Continue** (or Play when nothing is
started), **Shuffle**, **Mark watched**; a season row (pills), then the season's episodes with
progress. A season's page (selected from the pill with Select or opened from a season card) has
Play, Shuffle and Mark season watched.

**A collection** and **a playlist**: art (a collage of the first four posters when none), name,
count, total length, kind badge; **Play**, **Shuffle**; the items in order.

### 6.5 Remote and focus

| | Android TV (D-pad remote) | Apple TV (Siri Remote) |
|---|---|---|
| Open a library | focus lands on the first tab (Recommended); Up reaches Play and Shuffle | same; the header's buttons are above the tab bar in the focus order |
| Move between tabs | Left / Right on the tab row; Down enters the tab's content | swipe or click left / right on the tab bar; swipe down enters |
| Back | from content: to the top of the tab and its first row; again: to the tab row; again: leave the library | Menu (Back) the same three steps |
| Play the focused card | Play/Pause key plays that video at once (resumes if started); on a series card, Continue; on a collection or playlist card, Play | Play/Pause the same |
| More on a card | long Select or the Menu key: Play next, Add to queue, Add to playlist, Go to series, Mark watched / unwatched | long press: the same, as a context menu |
| Letter jump | a rail at the grid's end side; Right from the grid's last column reaches it | the same rail; or type a letter with the remote's keyboard search |
| Shuffle again | in the player's overlay, a Shuffle again button when the queue is shuffled | the same, in the info panel |
| Arabic | the whole layout mirrors; Left and Right swap; the letter rail lists ا…ي then A…Z | the same (SwiftUI's layout direction) |
| Focus memory | a tab keeps its focused item and scroll when the user leaves and returns | the same (`.focusSection`, `@FocusState` restored) |

A focused card scales by 1.08 with the theme's brass ring (Epic 48's tokens); 48 dp (Android) or
the system's focus effect (tvOS) is the minimum target.

### 6.6 Players and web

The phone and tablet Players (Android, Apple) get Play all and Shuffle on the same five sources,
Collections and Playlists tabs on a library, the server queue and its Up next (56.12). The web
moves its queue from `sessionStorage` to the server queue, gains the smart playlist editor, the
collection settings and `/big` Play and Shuffle (56.13).

## 7. Phases

| Phase | Stories | Parallel? |
|---|---|---|
| 1. Server | 56.01 queue, 56.04 server part (`group=series`), 56.05 server part (categories), 56.09 smart playlists, 56.14 OpenAPI and role tests | yes, each its own files; 56.14 last |
| 2. Automatic collections | 56.06 YouTube, 56.07 Plex, 56.08 server part (mode, flags, rows) | yes |
| 3. TV apps | 56.02, 56.03, 56.04, 56.05, 56.08, 56.10, 56.11 on Android TV and Apple TV | per platform in parallel; 56.03 first on each |
| 4. Players and web | 56.12, 56.13 | yes |

## 8. Stories

| # | Story | Platforms |
|---|---|---|
| 56.01 | [A play queue on the server](story-56-01-play-queue.md) | API |
| 56.02 | [Play and Shuffle on the TVs](story-56-02-tv-play-shuffle.md) | Android TV, Apple TV |
| 56.03 | [Library tabs and remote navigation on the TVs](story-56-03-tv-library-tabs.md) | Android TV, Apple TV |
| 56.04 | [Series grouping, series and season pages on the TVs](story-56-04-series-grouping.md) | API, Android TV, Apple TV |
| 56.05 | [Sort, filter, letter jump and Categories on the TVs](story-56-05-sort-filter-categories.md) | API, Android TV, Apple TV |
| 56.06 | [Automatic collections from YouTube playlists and channels](story-56-06-youtube-collections.md) | pipeline, API |
| 56.07 | [Plex collections, read into Maktaba](story-56-07-plex-collections.md) | pipeline, API |
| 56.08 | [Collections on the TVs, collection mode and Recommended rows](story-56-08-tv-collections.md) | API, web admin, Android TV, Apple TV |
| 56.09 | [Smart playlists](story-56-09-smart-playlists.md) | API, web |
| 56.10 | [Playlists on the TVs](story-56-10-tv-playlists.md) | Android TV, Apple TV |
| 56.11 | [Continue and Up next on the TVs](story-56-11-tv-continue-up-next.md) | Android TV, Apple TV |
| 56.12 | [The phone and tablet Players catch up](story-56-12-players-parity.md) | Android Player, Apple Player |
| 56.13 | [The web on the server queue, with smart playlists](story-56-13-web-parity.md) | web, web `/big` |
| 56.14 | [OpenAPI and role tests for the library routes](story-56-14-openapi-roles.md) | API, `shared/api` |

## 9. Where the Players and the web match the TVs

| Feature | TVs | Phone and tablet Players | Web | Web `/big` |
|---|---|---|---|---|
| Play all / Shuffle on five sources | 56.02 | 56.12 | 56.13 (has them, moves to the server queue) | 56.13 |
| Five library tabs | 56.03 | Videos, Series, Collections, Playlists (Recommended is the library page's hub above; Categories is the filter sheet) | has Home, Browse, Series, Collections; gains Playlists | 56.13 |
| Series grouping | 56.04 | has it | has it | has it |
| Collections | 56.08 | 56.12 | has them; gains settings | 56.13 |
| Smart playlists: make | no (made on the web or a Player) | 56.12 (from the filter sheet: Save as smart playlist) | 56.09 / 56.13 | no |
| Smart playlists: play | 56.10 | 56.12 | 56.13 | 56.13 |
| Server queue, Play next, Add to queue | 56.01, 56.11 | 56.12 | 56.13 | 56.13 |

## 10. Acceptance (the epic)

- On both TV apps, with the remote alone and in English and Arabic: open a library, Shuffle it,
  and the player plays random videos from the whole library, not one page; the overlay's
  Shuffle again reorders it.
- A series' Continue plays its next unwatched episode and then the following ones; a season's
  Shuffle plays only that season.
- A YouTube playlist that a library's videos came from shows as a collection in that library;
  a Plex collection on a library Maktaba indexes shows too, with its Plex order.
- A smart playlist "unwatched, speaker X, newest first, 50" made on the web plays on the TV and
  shrinks as its videos are watched.
- A queue started on the phone is offered as Continue on the TV's Home.
- A viewer cannot change a collection, a library's collection mode or another user's queue or
  playlist (403 / 404, role tests).
- Every string below each story exists in English and Arabic on each platform it names, and each
  platform's missing-Arabic test passes.

## 11. Decisions (2026-11-05)

1. **The queue lives on the server** (not in each app), because a TV must shuffle a whole library,
   and a queue should move between devices. Rejected: a client-side queue per app (today's web),
   which cannot shuffle past one page.
2. **Shuffle by a seeded hash**, not by materializing a shuffled copy of the library: uniform,
   pageable, and cheap on a 150,000-video home.
3. **Collections stay admin-made**; viewers get smart playlists. Rejected: user collections, which
   would duplicate playlists and break the API's admin-only rule for collections.
4. **Automatic collections are kept in step by their source**: an admin can rename, hide, promote
   or re-sort one, not edit its members (to change members, duplicate it as a manual collection).
5. **Series Play starts at the next unwatched episode**, as Plex; Shuffle includes watched ones.
6. **The five tabs keep Plex's names**, because people moving from Plex know them; Categories
   holds Maktaba's own facets.
7. **Library Play and Shuffle follow the Library tab's view**, so a filter (Unwatched, a speaker)
   narrows what plays.
