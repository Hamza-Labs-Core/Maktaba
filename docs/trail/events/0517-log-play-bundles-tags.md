---
schema: 1
kind: pr
pr: 517
title: Signed Google Play bundles on every tag; one package per Android app
date: 2026-11-01
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
`play-bundles.yml` attaches signed Google Play bundles to every `v*` tag: `maktaba-{player,remote,tv}-<version>.aab`, signed with the upload key from `ANDROID_UPLOAD_KEYSTORE_BASE64` / `_KEY_ALIAS` / `_KEY_PASSWORD` (and optional `_STORE_PASSWORD`); unsigned only as a workflow artifact. Docs: `docs/google-play.md`.
The Android Player and Remote lost their `phone`/`foldable`/`tablet` flavours. The Android gates now build an unsigned `bundleRelease`, so R8 breaks show in the PR.

## What we learned
- Decided: the flavours differed only in the `.foldable`/`.tablet` package, and Play makes each package its own app. The `X-Maktaba-Client` device now comes from the screen (`ClientHeader.deviceFor`: hinge sensor = foldable, smallest width ≥ 600 dp = tablet). Rejected: keeping the flavours for dev with a base-package release build.
- Gradle tasks are now `testDebugUnitTest`, `verifyRoborazziDebug`, `lintDebug`; the earlier `testPhoneDebugUnitTest` advice (#460, #466) no longer holds.
- Decided: versionCode = MAJOR*1000000 + MINOR*1000 + PATCH (`tools/release/play-version.sh`, tested). A pre-release shares its version's code, so the optional Play upload (`vars.PLAY_UPLOAD == 'true'` plus `PLAY_SERVICE_ACCOUNT_JSON`, internal track) skips pre-releases.
- Emulators on this Mac hang at boot headless with the android-37.0 and android-36 google-tv images (exit 139); an android-35 google_apis AVD boots.
- bundletool works from the Gradle cache's jar with a hand-built classpath (guava -jre, dagger 2.28.3, `--aapt2` from build-tools), or use the universal APK from `build-apks`.

## Open
- `LibrariesSectionTest.everyControlHasWordsForTalkBack` failed once under load (R8 + lint in parallel) and passed alone: a load-sensitive flake to fix at the root.
- For the owner: make the upload key (keytool in `docs/google-play.md`) and add the secrets; create the three apps in Play Console and upload each first bundle by hand; later turn on `PLAY_UPLOAD`.
