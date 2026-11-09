# Story 58.05 — Fire TV on Fire OS: an Amazon build of the Android TV app

> Epic 58 · Phase 2 · Depends on: 58.01, 58.02, 58.04 · Needed by: 58.14, 58.16

## Why

Fire OS is Android:
- Fire OS 7 is API 28, Fire OS 8 is API 30, Fire OS 14 is API 34 and Fire OS 16 is API 36.
- Amazon sets only a minimum targetSdk per Fire OS version, and no maximum (research.md §2.1).
- The Android TV app (`apps/tv/android`, minSdk 28, Compose for TV, Media3) uses no Google Play
  Services, Play Billing or Firebase. Its only Google-specific part is the Play bundle and
  `play-bundles.yml`.

So the cheapest new platform is the same app, built for Amazon's Appstore. Fire TV holds about
6% of TVs worldwide and is second only to Roku as a US streaming stick (research.md §7). The
Amazon Appstore for Fire TV is free to publish to, and its closure on Android phones
(2025-08-20) did not touch Fire TV.

Vega OS is Amazon's Linux-based Fire TV OS. It is not Fire OS: Android apps do not run on it.
It is story 58.18.

## Description

**One codebase, two flavors.** `apps/tv/android` gains a `store` flavor dimension with `google`
(today's build, unchanged) and `amazon`.

The `amazon` flavor:
- **Manifest.** It declares `android.hardware.touchscreen required=false` (already there) and
  `android.software.leanback required=false`. It declares both `LEANBACK_LAUNCHER` and `LAUNCHER`
  intent filters, since Amazon's pages do not settle which one Fire OS reads.
- **No Google code.** A Gradle check (`:app:checkAmazonDependencies`) fails the build if the
  `amazonRelease` runtime classpath holds any `com.google.android.gms`, `com.google.firebase` or
  `com.android.billingclient` artifact. It is wired into `check`, so a future library that
  pulls Play Services in breaks CI, not a Fire TV.
- **Player.** It uses stock Media3, the same version as the Google build.
  - Amazon recommends its patched port (`amzn/media3-external-port`) for device fixes. The story
    starts on stock Media3 and moves to the port only if the real-device checklist (T06) shows a
    fault it fixes. One player for both flavors halves the test matrix.
  - Tunnel mode stays off.
- **Playback capabilities.** It reports `device_caps` from `MediaCodecList` and
  `Display.getHdrCapabilities` (58.02) with profile `firetv`. The `google` flavor does the same
  with profile `androidtv`, which fixes the same model-by-model gap on Google TV devices.
- **Voice and search.** It wires Fire TV's search integration only through the standard Android
  `SEARCH` intent the app already handles. Alexa's video skill integration needs a catalog feed,
  which a self-hosted home cannot give Amazon, so it is left out (decision in the README).
- **Updates.** The Appstore updates installed apps on its own. The compatibility contract (58.01)
  covers the days between a home's release and the Appstore's.
- **Client header.** It sends `tv/<version> (firetv; tv)`.

**Release.** A `v*` tag builds `maktaba-tv-<version>-amazon.apk` (signed with a separate Amazon
upload key held in the release secrets) next to the Play bundle. A script then submits it through
Amazon's App Submission API as a new version of the live app, with the store listing kept in
`docs/stores/amazon.md` (58.04). A new version goes first to Live App Testing (Amazon's beta
channel, invited testers) and is promoted to the store by a person after the real-device
checklist passes.

**Home reach:** the same as Android TV, from the shared core:
- mDNS (Android NSD works on Fire OS);
- saved homes and a typed address;
- the cloud code of 58.03, added to the shared core so Android TV gains it too.

## Acceptance criteria

- **Given** a Fire TV Stick 4K Max on Fire OS 8, **when** the Amazon build is installed, **then**
  it finds the home by mDNS, signs in by the home's code or a cloud code, and plays a 4K HEVC
  HDR10 MKV in direct play.
- **Given** a Fire TV Stick on Fire OS 7, **then** the same build installs and plays its baseline
  (H.264 1080p).
- **Given** a dependency added to `core` that pulls in `play-services-base`, **then**
  `:app:checkAmazonDependencies` fails CI.
- **Given** the Back key on any top-level screen, **then** the app leaves to the Fire TV home, as
  Amazon's tests check.
- **Given** Arabic as the app language, **then** every screen is mirrored, as in the Google build.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Gradle | `amazonRelease` classpath | `checkAmazonDependencies` | passes; a fixture module with `play-services-base` fails it |
| T02 | Robolectric + Roborazzi (existing `_android-tv.yml`) | `amazon` flavor | screenshots of Home, Library, Player overlay, en and ar, dark | match references |
| T03 | Kotlin unit | recorded `MediaCodecList` answers of three Fire TV models | build `device_caps` | the vectors of 58.02 T07 |
| T04 | Instrumented, Android TV emulator API 30 **without Google APIs** (`system-images;android-30;android-tv;x86_64`) | `amazonDebug` APK, mock home | sign in by code, play a clip, Back to launcher | passes; logcat attached to the CI report |
| T05 | Manifest test | merged `amazonRelease` manifest | parse | both launcher filters; touchscreen and leanback not required |
| T06 | Real-device checklist (written, `docs/stores/amazon.md`) | Fire TV Stick 4K Max (Fire OS 8), one Fire OS 7 device | each release candidate before promotion from Live App Testing | sign-in, 4K HDR10, DV p8, EAC3 passthrough, Arabic, sleep and wake mid-video |

**What is honest about Fire TV testing:**
- There is no Fire OS emulator (research.md §2.1).
- T04 runs on Google's Android TV image without Play, which proves the app needs no Play
  Services. It does not prove Amazon's codecs, HDR or remote behaviour.
- Those come only from T06, on real devices, before each promotion.

## Strings

None new: the Amazon build uses the Android TV app's catalogs (English and Arabic) as they are.
58.01's `compat.updateApp.firetv` is the only flavor-specific string, chosen by flavor.

## Edge cases

- **Fire TV Cube's far-field Alexa saying "play X on Maktaba"**: not supported without a video
  skill. The app's search screen works with the remote's voice button through the `SEARCH`
  intent.
- **Fire TV Edition smart TVs** (Fire OS on a TV's own chip): the same APK. Its `device_caps` comes
  from the TV's decoders.

## Build plan

| File | Change |
|---|---|
| `apps/tv/android/app/build.gradle.kts` | `store` flavors; `checkAmazonDependencies` task |
| `apps/tv/android/app/src/amazon/AndroidManifest.xml` | launcher filters, features |
| `apps/android/core/…/playback/DeviceCaps.kt` (new) | the report, shared with the phone Player |
| `.github/workflows/_android-tv.yml` | amazon flavor build, T01, T04 on the no-Google image |
| `.github/workflows/tv-release.yml` | the Amazon APK; submission script `tools/release/amazon_submit.py` |
| `docs/stores/amazon.md` | listing, checklist, Live App Testing steps |
