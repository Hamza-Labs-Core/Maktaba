---
schema: 1
kind: pr
pr: 787
title: Plugin panels on the native series, library and TV pages (Epic 54, story 54.20)
date: 2026-11-07
session: 28c0b780-1dd6-4ba0-b27f-a3382e1c1cd1
issue: 741
---

## What happened
- Android and Apple show plugin panels on series and library pages. A chip opens Search with its text sent (Android `Route.SearchFor`, Apple `PlayerRoute.search`).
- The Android TV and Apple TV video pages show kv, badges and markers, open; a marker plays from its time.
- Screenshots of a panel and its TV subset (en/ar, 100 % and 200 %). maktaba.dev's plugin guide says where panels show and what a tap does.
- The web stage titles test's floor dropped from more than 50 to more than 30. Since #760 the graph holds 50 stages, so main failed the test.

## What we learned
- In SwiftUI, a `.task` on a view that draws nothing (an `if` with no content yet) never starts. `SubjectPanels` loaded nothing until its task moved to a stack that always exists. Only the iPhone UI test showed it: the build and `swift test` passed.
- A test's lower bound on a count another epic shrinks (Phase 4 moves stages into plugins) breaks main for everyone. Such a floor should only catch a pattern that no longer matches.

## Keep doing
- Run one real UI test for each new native page, not only a compile.

## Stop doing
- Filtering Gradle output through `grep "^e:"` with `-q` to judge a compile. It hid a failed build; read the exit status instead.

## Open
- None of #741's items are left after #783 and this PR.
