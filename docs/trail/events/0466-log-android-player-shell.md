---
schema: 1
kind: pr
pr: 466
title: "48.15: the Android Player shell and test harness"
date: 2026-10-31
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
`player/…/shell/` holds `PlayerShell` (Material's `NavigationSuiteScaffold`: bar when compact or tabletop, rail when medium, permanent drawer when expanded with medium height; a phone on its side keeps the rail).
One Navigation Compose `NavHost` per section, kept by a `SaveableStateHolder`: each section's stack and scroll survive switching, re-select pops to root, Back at another section's root goes to the first.
Typed `Route`s (`video`, `series`, `library`, `channel`, `watch`, as on Apple) and `PlayerSection.enabled` = Libraries only.
`LibraryBrowser.kt` moved to `libraries/`; the Libraries section is that browser until 48.27. Placeholder screens live in each screen's own folder (`home/`, `video/`, `search/`, `live/`, `profile/`, `watch/`), so the screen stories only replace their own file.
Harness: Robolectric 4.17 + Compose UI tests + Roborazzi on the JVM, with a `FakeHome` (MockWebServer) in `player/src/test/…/harness/`. CI runs `:player:testPhoneDebugUnitTest` and `:player:verifyRoborazziPhoneDebug`; references in `player/src/test/screenshots/`.

## What we learned
- Decided: one reachable section shows no navigation at all (a bar of one is no choice), so the shipped app looks as before until a second screen merges.
- Robolectric's SDK 36 needs Java 21; CI builds with 17, so `robolectric.properties` pins `sdk=35`. The references were recorded on the Mac, with a 2 % pixel tolerance for Linux text.
- Core's `TranslationsTest` reads only `strings.xml`; the per-screen `strings_<screen>.xml` tables are checked by the new `PlayerTranslationsTest` (and lint).
- With many agents building at once, the Gradle daemon died of GC thrashing; pass `-Dorg.gradle.jvmargs=-Xmx6g`.
- Review fixes: the Libraries browser's state is kept per signed-in home (`PlayerHome.keep`), so a section switch keeps its open video, library and search, and re-selecting Libraries closes the video (`OnReselect`).
- `PlayerNavigator.back()` never pops a section's root: Navigation Compose will pop it and leave the section blank.
- `FakeHome.requests` is a locked copy.
