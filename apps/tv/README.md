# `apps/tv/` — TV apps (Epic 14)

Native TV clients for the Maktaba media platform. Unlike the mobile and
desktop shells (which wrap the `web/` SPA via Capacitor / Tauri), the TV
apps are **fully native** — a 10-foot UI can't reuse a touch-oriented web
view — but they talk to the **same REST API** as every other client.

| Subdir | Platform | Stack | Build |
|---|---|---|---|
| [`tvos/`](tvos/) | Apple TV (tvOS 17+) | SwiftUI + AVKit | `make tv-build-ios` |
| [`android/`](android/) | Android TV / Google TV (minSdk 28) | Compose-for-TV + Media3 | `make tv-build-android` |

Bundle / application id: **`com.hamzalabs.maktaba.tv`** on both.

## What "10-foot UI" means here

A viewer sits ~3 m away and drives the app with a **D-pad / Siri remote**,
not a pointer. That single constraint shapes both apps identically:

- **Focus engine, not hit-testing.** The OS moves *focus* between
  on-screen elements; the app makes the focused element visually loud.
  - tvOS: `.focusable()` + `@FocusState`, reacting with a scale-up + ring.
  - Android TV: `androidx.tv:tv-material` `Card`s (focus scale/border
    built in) inside `androidx.tv:tv-foundation` `TvLazyRow` / grids.
- **Big type + safe-area gutters.** Larger base font sizes and ~48–80 px
  edge gutters to clear TV overscan.
- **Dark theme.** Living-room friendly; reduces glare and burn-in.

## Shared shape across both apps

Both implement the same screens against the same endpoints:

| Screen | Endpoint(s) |
|---|---|
| Home (Continue Watching + rails) | `GET /api/recommendations` |
| Libraries (grid) | `GET /api/libraries` |
| Media grid (library contents) | `GET /api/libraries/{id}/items` |
| Search | `GET /api/search?q=` |
| Player (HLS) | `GET /api/stream/{id}/index.m3u8` |
| Sign-in / refresh | `POST /api/auth/{login,refresh}` |

**Auth.** JWT access/refresh tokens, stored in the platform secure store
(tvOS Keychain / Android `EncryptedSharedPreferences`), injected as a
`Bearer` header, with a single-flight **401 → refresh → retry** path.

**Playback.** HLS via the platform-native player (AVKit `VideoPlayer` /
Media3 `ExoPlayer`), so the OS supplies the transport bar, scrubbing, and
subtitle/audio selection.

## Server connection

Neither app hard-codes a server. The base URL is set in-app under
**Settings → Server URL** (default `https://demo.maktaba.app`) so a
household can point the TV at a self-hosted or cloud Maktaba instance.

## Per-platform details

See [`tvos/README.md`](tvos/README.md) and
[`android/README.md`](android/README.md) for the directory layout, build
prerequisites, and the not-yet-scaffolded follow-ups (Siri / Assistant
voice intents, Top Shelf / recommendation channels, branded artwork).
