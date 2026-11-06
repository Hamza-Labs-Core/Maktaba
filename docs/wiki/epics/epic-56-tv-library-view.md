# Epic 56 — The TV library view: Play all, Shuffle, series, collections and playlists

> **Status:** spec (2026-11-05). **Source:** [`specs/epics/56-tv-library-view/`](../../../specs/epics/56-tv-library-view/README.md).
> **Anchors:** [Epic 7](epic-07-api-server.md) (collections) · [Epic 48](../../../specs/epics/48-native-players/README.md) (the native Players and TV apps) · [research](../../../specs/epics/56-tv-library-view/research.md).

## Goal

A library on Android TV and Apple TV should work like a library on Plex's TV apps: five tabs
(Recommended, Library, Collections, Playlists, Categories), Play all and Shuffle on a library, a
series, a season, a collection and a playlist, and browsing by series, collections and playlists,
all with the remote alone and in English and Arabic.

Today a TV library is one grid of videos, newest first. The server already has series, seasons,
collections and playlists; the web and the phone Players show part of them. What the server lacks
is a play queue: the web keeps its queue in one browser tab, and Shuffle reads one random page.

## Key decisions

| | |
|---|---|
| Play queue | on the server (slot 0190): made from a source, shuffled by a seeded hash so a whole library shuffles, materialized 500 items at a time, Play next and Add to queue, continued on any device, deleted after 7 idle days |
| Tabs | Plex's five names; Categories holds Maktaba's speakers, topics, languages and years |
| Series | the Library tab shows Videos or Series; series Play starts at the next unwatched episode |
| Collections | admin-made (manual, smart) as today, plus automatic ones from YouTube playlists, YouTube channels and Plex's collections (slot 0192), kept in step by their source; collection mode per library; promoted rows |
| Playlists | per user, manual and smart (slot 0191); viewers make smart playlists, not collections |
| Up next | the queue's next item with a 10 s countdown, on by default |
| Players and web | the same Play, Shuffle, queue, collections and playlists; the web moves off `sessionStorage` |
| Migrations | 0190–0192 planned; taken at merge time per `MANIFEST.md` |

## Stories

| # | Story | Phase |
|---|---|---|
| 56.01 | [A play queue on the server](../../../specs/epics/56-tv-library-view/story-56-01-play-queue.md) | 1 |
| 56.02 | [Play and Shuffle on the TVs](../../../specs/epics/56-tv-library-view/story-56-02-tv-play-shuffle.md) | 3 |
| 56.03 | [Library tabs and remote navigation on the TVs](../../../specs/epics/56-tv-library-view/story-56-03-tv-library-tabs.md) | 3 |
| 56.04 | [Series grouping, series and season pages on the TVs](../../../specs/epics/56-tv-library-view/story-56-04-series-grouping.md) | 1, 3 |
| 56.05 | [Sort, filter, letter jump and Categories on the TVs](../../../specs/epics/56-tv-library-view/story-56-05-sort-filter-categories.md) | 1, 3 |
| 56.06 | [Automatic collections from YouTube playlists and channels](../../../specs/epics/56-tv-library-view/story-56-06-youtube-collections.md) | 2 |
| 56.07 | [Plex collections, read into Maktaba](../../../specs/epics/56-tv-library-view/story-56-07-plex-collections.md) | 2 |
| 56.08 | [Collections on the TVs, collection mode and Recommended rows](../../../specs/epics/56-tv-library-view/story-56-08-tv-collections.md) | 2, 3 |
| 56.09 | [Smart playlists](../../../specs/epics/56-tv-library-view/story-56-09-smart-playlists.md) | 1 |
| 56.10 | [Playlists on the TVs](../../../specs/epics/56-tv-library-view/story-56-10-tv-playlists.md) | 3 |
| 56.11 | [Continue and Up next on the TVs](../../../specs/epics/56-tv-library-view/story-56-11-tv-continue-up-next.md) | 3 |
| 56.12 | [The phone and tablet Players catch up](../../../specs/epics/56-tv-library-view/story-56-12-players-parity.md) | 4 |
| 56.13 | [The web on the server queue, with smart playlists](../../../specs/epics/56-tv-library-view/story-56-13-web-parity.md) | 4 |
| 56.14 | [OpenAPI and role tests for the library routes](../../../specs/epics/56-tv-library-view/story-56-14-openapi-roles.md) | 1 |
