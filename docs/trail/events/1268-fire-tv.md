---
schema: 1
kind: pr
pr: 1268
title: "Epic 58 story 58.05: Fire TV as the Amazon build of the Android TV app"
date: 2026-11-14
session: 6cf0b355-9291-4a2d-a090-11a020ae3b58
agent: claude-code
---

## What happened
- #1268 merged as 7ae7fdd41. The Android TV app has a `store` flavor dimension: `google` (the
  Play build, the default) and `amazon` (Fire TV). Task and output names gained the flavor
  (`testGoogleDebugUnitTest`, `bundleGoogleRelease`, `app-google-release.aab`,
  `assembleAmazonRelease`), and CI, `play-bundles.yml`, `tv-release.yml`, the Makefile and
  affected-tests follow them.
- The Fire TV build carries no Google Play code: `checkAmazonDependencies` walks the release
  runtime graph and names the path to any gms, firebase or billing module, and CI proves it
  with a run that adds `play-services-base` (`-Pmaktaba.gmsFixture`) and must fail.
  `checkAmazonManifest` checks the merged manifest (Leanback and touchscreen not required,
  `amazon.hardware.fire_tv`, LEANBACK_LAUNCHER and LAUNCHER).
- `X-Maktaba-Client` names the store build (`tv/<v> (firetv; tv)`), and each stream open
  sends 58.02's `device_caps` read from MediaCodecList, the display's HDR types and Media3's
  AudioCapabilities, merged after #1239. An older home that refuses the field with 400
  `unknown field "device_caps"` is asked again without it, and then never sent it.
- A new CI job runs an instrumented journey on the Android TV emulator: sign in by code, play
  a clip, Back. `tv-release.yml` signs and stages the Amazon APK as a draft edit
  (`tools/release/amazon_submit.py`). Its `commit` step is gated by the real-device checklist
  in `docs/stores/amazon.md`.
- The journey test found a real bug: `TvApi` and `PlayQueue` sent `X-Maktaba-Client: native`.
  `TvApiTest` had asserted `native`, which passed only because no test set the header.

## What we learned
- The API 30 `android-tv` system image exists only as x86, not x86_64. Google's Android TV
  image has no `com.google.android.gms`, so it stands in for Fire OS without changes. There is
  no Fire OS emulator.
- On the Compose test clock, the code screen's polling advances only inside
  `rule.waitUntil`; a `Thread.sleep` loop stalls it. TV Material buttons ignore
  `performClick` and need `performSemanticsAction(SemanticsActions.OnClick)`. Code displays
  turn `I` into `1`, so a test code must avoid ambiguous letters.
- From Android 12, Back on the root activity moves the task back instead of finishing it, so
  a test must accept CREATED as well as DESTROYED.
- A step's `run:` script runs under `bash -e`. A command expected to fail ends the step at
  `out="$(cmd)"` unless the status is caught on that line (`|| status=$?`).
- Lint reads a flavor's manifest on its own, so the Amazon overlay repeats `exported`, the
  banner, LEANBACK_LAUNCHER and touchscreen even though the merge has them.
- affected-tests saw only `test`, `testDebug`, `testRelease` and `androidTest` as test sets.
  Flavored sets (`testAmazon`, `androidTestAmazon`) now count (`gradleSet` in
  `tools/affected-tests/classify.go`).
- Amazon's App Submission API cannot create or update Live App Testing versions; a commit
  goes to review and then the store. Hence a draft `stage` step on each tag and a `commit`
  step gated by the checklist.

## Keep doing
- Running the instrumented journey on a throwaway emulator before pushing. It found the
  header bug and four test-harness traps before CI.
- Running a workflow step's script locally with `bash -e` when the step expects a failure.

## Stop doing
- Asserting a default value (`native`) in a test of the code that should replace it.

## Open
- Owner actions: the Amazon developer account and listing, the Amazon upload key
  (`AMAZON_KEYSTORE_BASE64`, `AMAZON_KEY_ALIAS`, `AMAZON_KEY_PASSWORD`), an App Submission
  API security profile (`AMAZON_APPSTORE_CLIENT_ID`, `AMAZON_APPSTORE_CLIENT_SECRET`,
  `AMAZON_APPSTORE_APP_ID`), Live App Testing from the console, the real-device checklist
  on a Fire TV Stick 4K Max and a Fire OS 7 device, then `amazon_submit.py commit`.
- 58.03's cloud code (#1240, #1250) had not merged; the Fire TV build gets it with the
  Android TV app, and checklist row 1 covers it on a device.
- Amazon's reviewer notes need 58.04's review home and its password secret, not yet set up.
- `PairingTest.theSessionSignsInWithACode` failed once on Windows with a
  ConcurrentModificationException in the test's own map; it passed on a rerun.
