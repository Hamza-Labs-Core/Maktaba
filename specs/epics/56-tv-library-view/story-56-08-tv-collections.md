# Story 56.08 — Collections on the TVs, collection mode and Recommended rows

> Epic 56 · Phases 2 and 3 · Depends on: 56.03, 56.06 (slot 0192) · Needed by: —

## Why

Collections exist on the server (admin-made manual and smart ones, topic and clip groups, and now
YouTube and Plex ones) and on the web, but not on the TVs. Plex shows them in a Collections tab,
lets an admin choose whether they also show in the library grid (collection mode), and lets an
admin promote one to a row on Recommended and on Home.

## Description

### 1. The Collections tab (TVs)

A grid of collection cards from `GET /api/libraries/{id}/collections` (all kinds; hidden ones
left out for everyone, shown to admins on the web only). A card shows the art (the collection's
poster, else a collage of its first four videos' posters), the name (`name_override` first), the
count and a badge: YouTube playlist, YouTube channel, Plex, Smart, Topic, Clips. A **Kind**
button filters by kind (All, YouTube, Plex, Curated, Topics, Clips). Order: `sort_title`, else
name.

### 2. A collection page (TVs)

Art, name, count, total length, badge, description; **Play** and **Shuffle** (56.02); the items in
the collection's order (a smart one: its saved sort); focus starts on Play.

### 3. Collection mode (server and web admin)

`libraries.collection_mode` (slot 0192): `tab_only` (default: collections only in their tab),
`hide_items` (the Library grid shows a collection's card in place of its items), `show_both`
(cards and items). Only admin-made and Plex collections take part (YouTube channel collections
never replace items: they would hide most of a library). `GET /api/videos` honours it when
`library` is set and no filter other than sort is active; with a filter the grid shows items.
Set on the web's library settings (admin), `PATCH /api/libraries/{id}`.

### 4. Promoted rows

`collections.show_on_library` puts the collection as a row on the library's Recommended tab
(after the hub's rows, by `sort_title`); `show_on_home` puts it on Home (after pinned libraries).
Both set by an admin on the web's collection page; the TVs, the Players and the web read the rows
from `GET /api/libraries/{id}/hub` and `GET /api/home`.

### 5. Roles

Every change above is admin-only in the API (403 for viewers, with role tests); reads follow
`library_acl` (404 for a library the user cannot see).

## Acceptance criteria

- **Given** a library with 2 YouTube, 1 Plex and 1 curated collection, **when** its Collections
  tab opens on either TV, **then** 4 cards show with their badges, and Kind → Plex leaves 1.
- **Given** `collection_mode = hide_items` and a curated collection of 5 videos, **then** the Library
  grid shows the collection's card once and none of the 5; with the Unwatched filter, the items
  show and the card does not.
- **Given** an admin sets Show on Recommended on a collection, **then** both TVs show it as a row
  on that library's Recommended tab on the next open.
- **Given** a viewer, **when** they `PATCH /api/collections/{id}` or `PATCH /api/libraries/{id}`
  with `collection_mode`, **then** 403.

## Test cases

| ID | Type | Platform | Setup | Action | Expected |
|---|---|---|---|---|---|
| T01 | Go unit + Postgres | API | `hide_items` | `GET /api/videos?library=` | card in place of items; filter → items |
| T02 | Go unit | API | promoted | hub, home | rows present, ordered |
| T03 | Go role test | API | viewer, no session | PATCH collection, library | 403, 401 |
| T04 | unit (web) | web | library settings, collection page | change mode, flags | PATCH bodies; en/ar |
| T05 | Compose UI test | Android TV | collections mock | open tab; Kind → Plex | 4 cards; 1 |
| T06 | Compose UI test | Android TV | collection page | initial focus | Play |
| T07 | Compose UI test | Android TV | hub with promoted row | Recommended | row shown |
| T08 | XCUITest (`XCUIRemote`) | Apple TV | `MockHome` collections | open tab, select a card | collection page, Play focused |
| T09 | XCUITest | Apple TV | Arabic | collections tab | badges in Arabic, mirrored |
| T10 | Roborazzi | Android TV | tab and page en/ar dark | render | screenshots |
| T11 | i18n | all | new keys | missing-Arabic tests | pass |

## Strings

| Key | English | Arabic |
|---|---|---|
| `collections.kind` | Kind | النوع |
| `collections.kind.all` | All | الكل |
| `collections.kind.curated` | Curated | مختارة |
| `collections.kind.topics` | Topics | المواضيع |
| `collections.kind.clips` | Clips | المقاطع القصيرة |
| `collections.badge.smart` | Smart | ذكية |
| `collections.count` | {n} videos | {n} مقطع |
| `collections.length` | {h} h {m} min | {h} س {m} د |
| `collections.empty` | No collections in this library yet. | لا مجموعات في هذه المكتبة بعد. |
| `libraryAdmin.collectionMode` | Collections in the library grid | المجموعات في شبكة المكتبة |
| `libraryAdmin.collectionMode.tabOnly` | Only in the Collections tab | في تبويب المجموعات فقط |
| `libraryAdmin.collectionMode.hideItems` | Show the collection instead of its videos | اعرض المجموعة بدل مقاطعها |
| `libraryAdmin.collectionMode.showBoth` | Show the collection and its videos | اعرض المجموعة ومقاطعها |
| `collections.showOnLibrary` | Show on the library's Recommended tab | اعرضها في تبويب المقترحات للمكتبة |
| `collections.showOnHome` | Show on Home | اعرضها في الرئيسية |
| `collections.hide` | Hide this collection | أخفِ هذه المجموعة |
| `collections.rename` | Rename | إعادة التسمية |
| `collections.duplicate` | Duplicate as a curated collection | انسخها كمجموعة مختارة |

## Edge cases

- **A video in two collections under `hide_items`**: both cards show; the video hides once.
- **An empty collection** (all its videos deleted): left out of the tab and of rows.
- **Duplicate as curated** copies the current members into a new admin collection, which the
  source never changes again.

## Build plan

| File | Change |
|---|---|
| `api/internal/handlers/collections/collections.go` | PATCH fields; automatic ones refuse member edits (409 `automatic_collection`) |
| `api/internal/handlers/libraries/libraries.go` | `collection_mode` |
| `api/internal/handlers/videos/videos.go` | collection mode in the grid |
| `api/internal/handlers/browse/` (the hub and home handlers) | promoted rows |
| `web/src/pages/Collections/CollectionPage.tsx`, library settings | admin controls |
| `apps/tv/android/.../ui/screens/library/CollectionsTab.kt`, `ui/screens/collection/` (new) | TV |
| `apps/tv/tvos/MaktabaTV/Views/Collections/` (new) | TV |
| tests as in the table | |
