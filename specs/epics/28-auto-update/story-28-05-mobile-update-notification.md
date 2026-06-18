# Story 28.5 — Mobile update notification

> Epic 28 · Auto-Update · Phase 5 (mobile)

## Description

Store-distributed apps cannot self-update — Apple and Google own that.
So the mobile apps **detect** a newer version and **point the user at the
right place to get it**, without ever downloading a binary themselves.

- **Detection.** On launch (throttled to at most once per `check
  frequency`, default 24 h) the app compares its own version (28.1) with
  the latest applicable release. Two sources, in order:
  1. the connected Maktaba server's `GET /api/system/updates` (cheap,
     already channel-aware, no GitHub rate limit from the device), and
  2. a direct GitHub Releases check as a fallback when no server is
     paired.
- **Banner.** When newer, show a dismissible in-app banner: **"New
  version available — v1.4.2"** with a context-appropriate action:
  - iOS → **App Store** deep link;
  - Android (store build) → **Play Store** deep link;
  - Android (sideload build) → direct link to the `.apk` on the Release
    page.
  The banner also links to the release notes.
- **Settings.**
  - `check frequency` (off / daily / weekly),
  - `dismiss until next version` — dismissing hides the banner for the
    *current* latest version; it reappears only when a newer version than
    the dismissed one ships.

## Acceptance criteria

- **Given** the app is `v1.4.1` and the paired server reports `v1.4.2`
  available,
  **when** the app launches (and the throttle window has elapsed),
  **then** the banner shows "New version available — v1.4.2" with the
  store/`.apk` action and a notes link.

- **Given** the user taps the action,
  **when** on iOS,
  **then** the App Store page opens; **on Android store build** the Play
  Store opens; **on sideload build** the `.apk` download opens.

- **Given** the user dismisses the banner for `v1.4.2`,
  **when** the app relaunches and the latest is still `v1.4.2`,
  **then** the banner stays hidden; **when** `v1.4.3` later ships, the
  banner reappears.

- **Given** no server is paired,
  **when** the app checks,
  **then** it falls back to the GitHub Releases check and behaves
  identically.

- **Given** `check frequency` is off,
  **when** the app launches,
  **then** no check is made and no banner shows.

- **Given** the app is on the latest version,
  **when** it checks,
  **then** no banner shows.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | current=`1.4.1`, latest=`1.4.2` | compare | banner shown |
| T02 | unit        | current=`1.4.2`, latest=`1.4.2` | compare | no banner |
| T03 | unit        | dismissed `1.4.2`, latest `1.4.2` | render | hidden |
| T04 | unit        | dismissed `1.4.2`, latest `1.4.3` | render | shown |
| T05 | unit        | throttle window not elapsed | launch | no check |
| T06 | unit        | platform=ios | action url | App Store link |
| T07 | unit        | platform=android-sideload | action url | `.apk` link |
| T08 | integration | server reports update | launch | banner via server source |
| T09 | integration | no server | launch | banner via GitHub fallback |

## Edge cases

- **App ahead of release** (TestFlight/internal build newer than the
  public release). No banner (current ≥ latest).
- **Both sources disagree.** Prefer the server (it's channel-aware and
  authoritative for that deployment).
- **Offline.** Silent no-op; retried next eligible launch.
- **GitHub rate limit on the fallback.** Backoff; the server source is
  the primary path so this is rare.
- **Channel.** The device follows the paired server's channel via the
  server source; the GitHub fallback uses `stable` only (a phone
  shouldn't silently track beta).
- **Locale.** Banner text and the version string are i18n'd (EN + AR,
  RTL-aware).

## Files / packages

- `web/src/lib/update.ts` (new) — shared check/compare/throttle/dismiss
  logic (the mobile apps wrap the same `web/dist` SPA, so this is web
  code that activates on the native platforms).
- `web/src/components/UpdateBanner.tsx` (new).
- `web/src/lib/native.ts` — platform detection + store/apk link
  resolution (extends existing native helpers).
- i18n keys (EN + AR).

## Open questions

- **Push a notification when the app is closed?** Out of scope; relies on
  the on-launch check. Epic 25's push infra could drive this later.
