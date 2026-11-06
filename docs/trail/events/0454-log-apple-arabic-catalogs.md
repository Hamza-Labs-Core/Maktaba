---
schema: 1
kind: pr
pr: 454
title: Arabic in the Apple Remote, Player and TV
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
Remote, Player (iOS, Mac) and TV got English and Arabic String Catalogs; MaktabaKit has its own, loaded via `Bundle.module`.
"The home" is «الجهاز الرئيسي» in Arabic.
The guard `tools/apple/check-string-catalogs.py` runs in the new `apple-apps` CI job.
The hand-rolled Arabic in `ConnectStrings`, `PermissionsStrings`, `ErrorStrings` and `TVConnectStrings` was kept.

## What we learned
- `String(localized:bundle:locale:)` does not pick the language. Build a `LocalizedStringResource` with `.locale` (see `kit()` in `Localization.swift`).
- The shared iPhone 17 simulator crashed SpringBoard repeatedly. This work used a throwaway `maktaba-ar-test` on iOS 27.0 (iOS 27.1 is not compatible with that device type) and deleted it afterwards.
- Known failures unrelated to this work: run on iOS, MaktabaKit's `thisPlatformAsksForLocalNetwork` expects the Mac's list; an unsigned run fails the Mac Player keychain test.

## Keep doing
- Use a throwaway simulator per run and delete it afterwards.
