# Story 58.08 — The TV build of the big-screen view, served by the home at /tv/

> Epic 58 · Phase 3 · Depends on: 58.01, 58.02 · Needed by: 58.09, 58.10, 58.11, 58.14

## Why

Samsung's Tizen and LG's webOS both run web apps in Chromium. The same model year gets nearly
the same engine on both: 2024 has M108 on both, 2025 has M120 on both (research.md §3.1, §4.1).
Maktaba already has a 10-foot web UI: the big-screen view of Epic 46 (`web/src/bigscreen`, served
at `/big`):
- It covers Home, Libraries, Series, Video, Search, Live TV, Settings and the player.
- It has its own focus engine (`bigscreen/focus`), key and gamepad input (`bigscreen/input`),
  English and Arabic, and mirroring.

Two things stop it running on a TV:
- **It is built for modern browsers.** `web/vite.config.ts` targets `es2022`, which a 2020 TV
  (Tizen 5.5 is M69, webOS 5 is M68) cannot parse.
- **It does not yet have the TV layout of Epic 56.** That means the library tabs (Recommended,
  Library, Collections, Playlists, Categories), Play and Shuffle, series with seasons, and the
  letter rail that the Apple TV and Android TV apps get.

## Description

**One UI, two builds.**
- `/big` stays as it is, for desktops and browsers.
- A second Vite build, `build:tv`, outputs the same `bigscreen` code at `/tv/`:
  - target `chrome68`, with the needed polyfills (`core-js` usage-based, `whatwg-fetch` not
    needed at M68);
  - no `eval`, no top-level `await`, and CSS without `:has()` or container queries;
  - a check (`es-check es2017` plus a CSS feature lint) fails the build when the output uses
    something M68 cannot run.
- The home serves `/tv/` like `/big`, from the same release. The UI on a TV is therefore always
  the home's own version: a new home brings a new TV UI with no store review. Story 58.09 says
  how the packaged TV app loads it.

**What `/tv/` adds to the big-screen view.** Everything below lands in `bigscreen`, so `/big`
gains it too, and the desktop's big screen catches up with the TVs. These are Epic 56's screens,
matched to the Apple TV and Android TV layouts:

| Screen | From | Matches |
|---|---|---|
| A library: tabs Recommended, Library, Collections, Playlists, Categories; Play and Shuffle in the header | 56.03, 56.02 | Epic 56 §6.4 |
| Library tab: Show [Videos \| Series], Sort, Filter, the letter rail (A…Z and ا…ي) | 56.04, 56.05 | §6.4, §6.5 |
| A series: Continue, Shuffle, Mark watched, season pills, episodes | 56.04 | §6.4 |
| A collection, a playlist: Play, Shuffle, items | 56.06–56.10 | §6.4 |
| Continue and Up next in the player | 56.11 | §6.4 |
| The play queue on the server | 56.01 | §6.1 |

**Remote and focus** follow Epic 56 §6.5 key for key:
- Left and Right on the tab row; Down enters a tab.
- Back, three steps: to the top of the tab, then to the tab row, then out.
- Play/Pause on a focused card plays it.
- A long OK, or the platform's menu key, opens the card's actions.
- Each tab keeps its focus memory.

A focused card scales by 1.08 with the brass ring of Epic 48's tokens.

The TV's keys come from 58.09's bridge, which maps:
- Tizen's `tizen.tvinputdevice` keys: `MediaPlayPause`, `ColorF0Red` and others, registered at
  start;
- webOS's key codes;

onto `bigscreen/input/actions.ts`'s actions. The browser's own keys keep working for `/big`.

**What `/tv/` leaves out of `/big`:**
- the mouse pointer UI, except on webOS's Magic Remote, where `bigscreen` already handles the
  pointer;
- desktop-only Settings: start in big screen, and leaving big screen;
- anything admin, as `/big` already does.

**Performance budget on a 2020 TV:**
- first screen in under 4 seconds from launch on the LAN;
- under 3 seconds between screens;
- posters at `?size=small` (story 48.5);
- grids virtualized;
- no animation longer than 200 ms.

Measured on the low end of the real-device checklist (58.16).

**Player.** `/tv/` does not play video itself on a TV. It asks the launcher's bridge to play
(58.09), and the bridge drives AVPlay on Tizen (58.10) or the native `<video>` on webOS (58.11).
Outside a TV launcher (`/tv/` in a desktop browser, for tests) it falls back to `bigscreen`'s
existing hls.js player.

## Acceptance criteria

- **Given** `/tv/` in Chromium 68, **then** every screen of the table loads and runs (Playwright
  on a pinned M68-compatible Chromium is not available, so the es-check and CSS lint stand in;
  see Test cases).
- **Given** a library on `/tv/`, **when** the person presses Play, **then** the Library tab's
  current view plays in order on the server queue, as on Android TV.
- **Given** Arabic, **then** the layout mirrors, Left and Right swap, and the letter rail lists
  ا…ي then A…Z.
- **Given** `/big` in a desktop browser after this story, **then** it gains the library tabs and
  the series page, with no desktop regression (its existing tests pass).

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Build check | `build:tv` | `es-check es2017` + CSS lint for M68 | passes; a fixture using `?.` in raw output or `:has()` fails it |
| T02 | Vitest (existing `bigscreen` tests, extended) | focus engine, tabs, letter rail, queue actions | key sequences | Epic 56 §6.5 behaviours, en and ar |
| T03 | Playwright (Chromium, 1920×1080, `/tv/` against the mock home) | each screen | D-pad key presses only | screenshots dark, en and ar; console and network log in the CI report |
| T04 | Playwright, throttled CPU ×6 | Home → Library → Series | timing | within the budget above |
| T05 | Go unit | the home's static server | GET `/tv/`, `/tv/assets/…` | served with long cache on hashed assets, `no-cache` on `index.html` |

## Strings

`/tv/` uses `web/src/i18n` (English and Arabic); the new screens' keys come from Epic 56's web
stories (56.13). TV-only keys:

| Key | English | Arabic |
|---|---|---|
| `tv.exitConfirm` | Leave Maktaba? | الخروج من Maktaba؟ |
| `tv.exitConfirm.yes` | Leave | خروج |
| `tv.exitConfirm.no` | Stay | البقاء |

## Edge cases

- **A TV older than 2020** (Tizen 5.0 M63, webOS 4 M53): not supported. The launcher (58.09)
  checks the engine at start and shows that the TV is too old, naming Cast as the way to watch.
- **A home older than this story** has no `/tv/`: the launcher sees the 404 and shows Update the
  home (58.01).

## Build plan

| File | Change |
|---|---|
| `web/vite.config.ts` | `tv` mode: target, polyfills, base `/tv/` |
| `web/src/bigscreen/screens/` | Library tabs, Series, Collection, Playlist, letter rail |
| `web/src/bigscreen/input/` | `bridge` input source (58.09) |
| `cmd/maktaba-server/` static routes | `/tv/` |
| `tools/web/tvcheck.mjs` (new) | T01 |
