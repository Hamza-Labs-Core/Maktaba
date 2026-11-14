---
schema: 1
kind: pr
pr: 1244
title: "The apps say when they and the home need an update (58.01, app side)"
date: 2026-11-14
session: 8f145529-298a-4b09-bbbf-6a03b83bae53
agent: claude-code
---

## What happened
- #1244 merged as a0b5b696d. The Android phone and TV apps (the core's `AppSession`) and the
  Apple apps (MaktabaKit's `AppSession`) read `api_level`, `api_level_oldest` and
  `client_floor` before signing in and show **This home needs an update** or **Update this
  app**, in English and Arabic, with Try again and Choose another home. A home before the
  contract reads as level 0; the apps ship with `min_api_level` 0 and `built_for_api_level` 1,
  so they carry on with it.
- Connecting decides from the discovery answer it already has; at start a probe runs before
  the saved sign-in is used. A 426 `client_too_old` at sign-in, refresh or restore also shows
  Update this app. The check runs only when the app passes its identity (`compatApp` /
  `compat:`), so the existing session tests are unchanged.
- The first CI run failed `affected-tests`: `StartScreens.kt`, `StartViews.swift`,
  `CompatView.swift` and `PlayerApp.swift` had none of their mapped tests changed. The Apple UI
  tests (T05) had been left out; `PlayerCompatUITests` and `TVCompatUITests` were added, and a
  `No-Test-Change` trailer named the new tests the coverage map (built on main) did not know.
- The first run also failed the Player UI shard 1/6 on iPhone and iPad:
  `PlayerParentalUITests.testTimeUpAtTheStartInArabic` reached the time's-up screen and then
  waited past 10 s for **Ask for more time**. The re-run of the failed jobs passed.
- The Android TV screenshots were 1.4 MB at full size; Roborazzi's `resizeScale = 0.5` brought
  them to about 390 KB.

## What we learned
- `affected-tests` maps a source to its tests through the coverage map built on main, so a new
  test file never counts for an existing source in the pull request that adds it; a
  `No-Test-Change` trailer naming the new tests is the honest way through.
- The apps' Arabic says الجهاز الرئيسي for the home and مكتبة for Maktaba; a story's table may
  use other words, and the app vocabulary wins.

## Keep doing
- Writing each TV app's UI test (T05) in the same pull request as its page, even when it can
  run only in CI.

## Stop doing
- Treating a new screen's unit tests as enough for `affected-tests`: the screen's existing
  host files need their UI tests touched or named.

## Open
- `PlayerParentalUITests.testTimeUpAtTheStartInArabic` is flaky on its Ask for more time wait
  (10 s), seen on #1244 on both iPhone and iPad.
- The TypeScript and BrightScript runs of `shared/compat/vectors.json` and the web-TV and Roku
  UI tests come with the web-TV launcher (58.09) and Roku (58.07).
