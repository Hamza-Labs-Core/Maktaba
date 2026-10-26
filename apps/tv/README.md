# `apps/tv/` — TV apps (Epic 14)

Native TV clients for the Maktaba media platform. Unlike the mobile and
desktop shells (which wrap the `web/` SPA via Capacitor / Tauri), the TV
apps are **fully native** — a 10-foot UI can't reuse a touch-oriented web
view — but they talk to the **same REST API** as every other client.

| Subdir | Platform | Stack | Build |
|---|---|---|---|
| [`tvos/`](tvos/) | Apple TV (tvOS 26) | SwiftUI + AVKit, on `apps/apple/MaktabaKit`; the "Maktaba TV" target of `apps/apple/Maktaba.xcodeproj` | `make tv-build-ios` |
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
- **"Islamic library" theme.** Dark "Ink & Brass" (background
  `#0B111C`, gold `#E3B252`) by default; Amiri Bold headings and IBM Plex
  Sans Arabic body text (SIL OFL, bundled with their licences). Home draws
  a library scene full-screen; text always sits on translucent reading
  panels or a gradient fade, never on the art. Focused cards get a 3px
  gold ring + scale. tvOS follows the system light/dark setting (light =
  "gold on white"); Android TV is dark-only.

## Shared shape across both apps

Both implement the same screens against the same endpoints:

| Screen | Endpoint(s) |
|---|---|
| Home (Continue Watching + rails) | `GET /api/recommendations` |
| Libraries (grid) | `GET /api/libraries` |
| Media grid (library contents) | `GET /api/libraries/{id}/items` |
| Search | `GET /api/search?q=` |
| Player (HLS) | `GET /api/stream/{id}/index.m3u8` |
| Sign-in by code (the TV shows a code / QR; a phone or the web approves at `/link`) | `POST /api/auth/device/{start,token}` |
| Sign-in / refresh | `POST /api/auth/{login,refresh}` |

**Auth.** A TV signs in with a code a signed-in person approves on a phone or
the web (`<home>/link`), or with a password as the fallback. JWT
access/refresh tokens, stored in the platform secure store (tvOS Keychain /
an Android Keystore key), injected as a `Bearer` header, with a single-flight
**401 → refresh → retry** path. The web port's `/stream/` wants the token on
every playlist and segment request too.

**Playback.** HLS via the platform-native player (AVKit `VideoPlayer` /
Media3 `ExoPlayer`), so the OS supplies the transport bar, scrubbing, and
subtitle/audio selection.

## Server connection

Neither app hard-codes a server. The TV finds homes on its network (mDNS
`_maktaba._tcp`) or takes a typed address, and remembers the homes it used.

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
and signs with `apksigner` when they're present. Without them the APK is
unsigned, so it is kept as the run's `maktaba-tv-android` artifact
(`maktaba-tv-<version>-unsigned.apk`) and not attached to the Release.
Build it locally with `make package-tv-android`.

**tvOS distribution is manual.** Apple does not allow unsigned tvOS app
distribution, and a shippable `.ipa` needs a full Xcode app target (not
just the SwiftPM core), an Apple Distribution certificate, and a tvOS
provisioning profile. CI therefore only compiles the SwiftUI sources
(`swift build`, the same check as `make tv-build-ios`); a maintainer
produces and uploads the release build in Xcode (**Product ▸ Archive ▸
Distribute App ▸ TestFlight**). When an Xcode target + signing material
are provisioned, the `xcodebuild archive`/`exportArchive` steps can be
added to the `tvos` job to automate it.

## Live TV at home and away (Epic 32 story 32.9, phase C — not built yet)

The web player (`web/src/pages/LiveTV/Player.tsx`, through the shared
`@maktaba/tvplayer` module) already chooses between the LAN stream and
Cloudflare. The native apps get live channels later and follow the same
contract; nothing here is implemented yet:

- **Which path.** The Epic 25.10 resolver picks the endpoint: a reachable
  LAN endpoint → LAN HLS from the home (`/stream/channel/{id}/index.m3u8`,
  signed in as today); the relay → the remote API. `GET /api/tv/info`'s
  `remote.via_relay` says the same from the server side. **Stream
  affinity:** a session stays on the path it started on until it ends.
- **Remote.** `POST /api/tv/remote/tune {channel_id, device}` → `200
  {session_id, hls_url, expires_at, refresh_after}` or `202 {retry_after}`
  (tune again with the same body until live); `POST
  /api/tv/remote/sessions/{id}/heartbeat` every 60 s; `DELETE
  /api/tv/remote/sessions/{id}` on stop and channel change. Errors: 402
  budget reached, 403 not allowed, 429 `user_streams_full` /
  `remote_channels_full`, 503 not set up — the same words as the web player
  (`live.remote.*`).
- **Token refresh.** AVPlayer / ExoPlayer cannot rewrite segment URLs
  cheaply, so at `refresh_after` the app takes the new `hls_url` from the
  heartbeat (or tunes again) and swaps the item at the next segment
  boundary (AVQueuePlayer / an ExoPlayer playlist), accepting a short
  re-buffer.
- **Guide.** The existing JSON APIs (`/api/channels`, `/api/channels/guide`,
  `/api/channels/now`) through the relay; XMLTV/M3U are LAN only.
- **Switching.** The same controls as the web player (Epic 34.6): CH+/CH−
  and the D-pad, number keys + OK, last channel, a mini-guide with now/next;
  keep the old channel playing until the new one can play.
