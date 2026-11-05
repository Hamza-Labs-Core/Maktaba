# Story 56.10 — Playlists on the TVs

> Epic 56 · Phase 3 · Depends on: 56.01, 56.03, 56.09 · Needed by: —

## Why

A user's playlists (manual and smart) exist on the server and the web, and under Profile on the
phone Players, but cannot be seen or played on a TV. Plex's TV apps show, in a library's
Playlists tab, the user's playlists with items from that library, and every playlist in a
top-level Playlists place.

## Description

### 1. Where

- **A library's Playlists tab**: `GET /api/me/playlists?library={id}` (new filter: manual lists
  with at least one item from the library; smart lists whose `libraries` include it or are all).
- **The Account tab** (Android TV `TvTab.Account`, tvOS Settings) gains **Playlists**: all of the
  user's lists.
- A playlist card: poster (a collage of the first four), name, count, Smart badge.

### 2. A playlist page

Name, count, total length, Smart badge and its rules in words (56.09); **Play** and **Shuffle**
(56.02); the items. Manual lists: on an item, the context action **Remove from playlist**; the
order is changed on the web or a Player (moving items with a D-pad is slow and error-prone).

### 3. Adding from the TV

The card context menu (README §6.5) has **Add to playlist**: a list of the user's manual
playlists and **New playlist** (a name typed with the TV keyboard; tvOS's system keyboard,
Android TV's IME). It calls `POST /api/me/playlists/{id}/items` or `POST /api/me/playlists`.
Smart lists are not offered.

## Acceptance criteria

- **Given** two manual lists, one with an item from library A, and a smart list over all
  libraries, **when** library A's Playlists tab opens, **then** it shows the first manual list and
  the smart list.
- **Given** a focused video card, **when** Add to playlist → New playlist "دروس" is chosen,
  **then** a playlist named دروس with that video exists, and a confirmation shows.
- **Given** a smart playlist, **then** its page has no Remove action and shows its rules.
- **Given** a playlist page, **when** Shuffle is pressed, **then** the queue is `kind: playlist`,
  shuffled.

## Test cases

| ID | Type | Platform | Setup | Action | Expected |
|---|---|---|---|---|---|
| T01 | Go unit + Postgres | API | lists and library | `?library=` | the right lists |
| T02 | Go role test | API | other user's list | `?library=` | never listed |
| T03 | Compose UI test | Android TV | mock | Playlists tab | cards; Smart badge |
| T04 | Compose UI test | Android TV | card menu | Add to playlist → existing | POST items |
| T05 | Compose UI test | Android TV | card menu | New playlist with Arabic name | POST with the name |
| T06 | XCUITest (`XCUIRemote`) | Apple TV | `MockHome` playlists | long press a card → Add to playlist | mock records POST |
| T07 | XCUITest | Apple TV | smart playlist fixture | open | rules shown; no Remove |
| T08 | XCUITest | Apple TV | Arabic | Playlists tab | mirrored, Arabic strings |
| T09 | Roborazzi | Android TV | tab, page, menu en/ar dark | render | screenshots |
| T10 | i18n | both | new keys | missing-Arabic tests | pass |

## Strings

| Key | English | Arabic |
|---|---|---|
| `playlists.title` | Playlists | قوائم التشغيل |
| `playlists.add` | Add to playlist | إضافة إلى قائمة تشغيل |
| `playlists.new` | New playlist | قائمة تشغيل جديدة |
| `playlists.newName` | New playlist name | اسم قائمة التشغيل الجديدة |
| `playlists.added` | Added to {name} | أُضيف إلى {name} |
| `playlists.remove` | Remove from playlist | إزالة من قائمة التشغيل |
| `playlists.count` | {n} videos | {n} مقطع |
| `playlists.empty.library` | None of your playlists has videos from this library. | لا تحتوي أيٌّ من قوائم تشغيلك على مقاطع من هذه المكتبة. |
| `playlists.empty.title` | No playlists yet | لا توجد قوائم تشغيل بعد |
| `playlists.addError` | Could not add to the playlist | تعذّرت الإضافة إلى قائمة التشغيل |

## Edge cases

- **The 200-list or 5,000-item cap**: the server's 422 shows as `playlists.addError` with its
  reason.
- **The same video added twice**: the server keeps one (as today); the confirmation still shows.

## Build plan

| File | Change |
|---|---|
| `api/internal/handlers/browse/playlists.go` | `?library=` |
| `apps/tv/android/.../ui/screens/library/PlaylistsTab.kt`, `ui/screens/playlist/` (new), card menu | TV |
| `apps/tv/tvos/MaktabaTV/Views/Playlists/` (new), `SettingsView.swift` | TV |
| tests as in the table | |
