---
schema: 1
kind: pr
pr: 910
title: A page's built strings follow its locale; the page references' bundle trap
date: 2026-11-07
session: 29e8f107-ee7f-4db2-a128-c02a2839240a
agent: claude-code
prs: [893]
---

## What happened
- #893's Arabic series reference read "3 episodes · 2024" in English. The Arabic catalog had the plural, and the app has no language of its own beyond the bundle's, so the shipped app was right: the cause was in how the page was drawn. A few strings the pages build as a `String` (a series' episode count and year, a card's detail, a video's length, date and season and episode) used `String(localized:)` and `.formatted()`, which take the process's language, while the page's `Text`s take SwiftUI's `\.locale`, which is all the snapshot sets.
- #910 makes those strings resolve with the page's `\.locale` (`LocalizedStringResource.string(in:)`), adds the hosted `PageLocale` suite, and writes Android's series year in the configuration locale's digits, as its episode-count plural is (`SeriesYearTest`). Web already formats with its locale.
- #893's Mac job failed after the video references were committed: they were not in the test bundle. xcodegen listed each PNG one by one, and the project had been generated before the video PNGs existed. `PlayerMacTests/Snapshots` became a folder reference, so any reference added to the folder ships with the bundle.
- #893's iPad 2/6 job passed all 21 tests and failed uploading its artifact (ENOTFOUND). A failed job can't be re-run until its whole run ends: a bounded script waited for the run, re-ran the failed jobs, then started the merge watcher.
- Slip: #893 carried its trail event inside the code PR. Trail events go in a separate trail-only PR; this one does.

## What we learned
- On Apple, a `String` built with `String(localized:)` or `.formatted()` follows the process and ignores SwiftUI's `\.locale`; a page that a preview or test draws in another language mixes the two. Build such strings with the environment's locale.
- #910's first CI run passed against the old Arabic references: a short phrase in another language changes about 700 to 3,900 pixels, under the 0.2 % tolerance of a 760×1500 page. The snapshots guard layout, not wording; the Arabic references were re-recorded from that run anyway, and the English ones, pixel-identical, were kept.
- `affected-tests` matches a changed source with a test named after it (`SeriesPage.kt` → `SeriesPageTest.kt`, `SeriesScreen.swift` → `SeriesScreenTests.swift`); #910's first push named its tests after the behaviour and was blocked. Name a new test after the file it guards.
- A re-recorded PNG can differ in bytes with no pixel change; compare pixels before committing so the diff holds only real changes.

## Keep doing
- Read a reference image for the thing it should guard, including its language, before committing it.

## Stop doing
- Listing reference files one by one in a generated project: a folder reference cannot leave a new one out.

## Open
- The snapshot tolerance lets a changed phrase through; a text-level check (or a smaller tolerance once its noise is measured) would catch wording.
