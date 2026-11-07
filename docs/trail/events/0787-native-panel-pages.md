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
- Main failed the web stage titles test after #760 left 50 stages in the graph. A lowered floor was committed here and reverted: #786 is the fix, reading the stages in first-party plugin manifests too.

## What we learned
- In SwiftUI, a `.task` on a view that draws nothing (an `if` with no content yet) never starts. `SubjectPanels` loaded nothing until its task moved to a stack that always exists. Only the iPhone UI test showed it: the build and `swift test` passed.
- When main fails a test because another epic moved things (Phase 4 moved stages into plugins), the fix is to follow them, not to loosen the check. A lower floor would have hidden moved stages with no titles.

## Keep doing
- Run one real UI test for each new native page, not only a compile.

## Stop doing
- Loosening a failing check on main inside an unrelated PR. Look for the PR that fixes it first.
- Filtering Gradle output through `grep "^e:"` with `-q` to judge a compile. It hid a failed build; read the exit status instead.

## Open
- None of #741's items are left after #783 and this PR.
