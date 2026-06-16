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

## CI build & packaging

[`.github/workflows/tv-release.yml`](../../.github/workflows/tv-release.yml)
runs on every `v*` tag. The TV apps are fully native (no `web/dist`), so
the two jobs are independent:

| Job | Runner | Steps | Artifact |
|---|---|---|---|
| `android-tv` | `ubuntu-22.04` | JDK 17 + Android SDK → `gradlew :app:assembleRelease` | `maktaba-tv-<version>.apk` (attached to the Release) |
| `tvos` | `macos-latest` | `swift build` (compile check) | *manual* — see below |

**Android TV signing** reuses the same secrets as the mobile workflow
(`ANDROID_KEYSTORE_BASE64` / `ANDROID_KEY_ALIAS` / `ANDROID_KEY_PASSWORD`)
and signs with `apksigner` when they're present; otherwise it ships the
unsigned release APK. Build it locally with `make package-tv-android`.

**tvOS distribution is manual.** Apple does not allow unsigned tvOS app
distribution, and a shippable `.ipa` needs a full Xcode app target (not
just the SwiftPM core), an Apple Distribution certificate, and a tvOS
provisioning profile. CI therefore only compiles the SwiftUI sources
(`swift build`, the same check as `make tv-build-ios`); a maintainer
produces and uploads the release build in Xcode (**Product ▸ Archive ▸
Distribute App ▸ TestFlight**). When an Xcode target + signing material
are provisioned, the `xcodebuild archive`/`exportArchive` steps can be
added to the `tvos` job to automate it.
