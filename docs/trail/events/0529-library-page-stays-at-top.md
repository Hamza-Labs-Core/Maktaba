---
schema: 1
kind: pr
pr: 529
title: Android Player library page opened scrolled on a slow home
date: 2026-11-02
session: unknown
source: claude-trail.md
---

## What happened
- PR #513's CI (run 41481083121) failed LibrariesScreenshotTest.libraryEnglish: `libraries_library_phone_dark_en` scrolled about 27 dp, Continue Watching cut at the top. A real user bug, not a flake.
- Cause: a lazy grid keeps its first visible item's key in place. When the videos answer before the hub, the first layout is [tabs, filters, videos] with `tabs` first; the hub rows then come in above it and the grid scrolls to keep `tabs` at the top, as far as its end allows, so the page opens part way down (27 dp on CI, 185 dp in the new test). Same in the Libraries grid: unpinning the first pinned library moved it to a later line and the grid followed it.
- Fix: `StaysAtTopWhen(key)` (libraries/GridTop.kt): when what leads the grid changes and the grid is at its top (index 0, offset 0), `requestScrollToItem(0)` before the next measure. A scrolled grid keeps its key as before. Used by LibraryPage (key: hub rows) and the Libraries grid (key: library order). The Series grid only appends, so it is not affected.
- Tests: LibrariesSectionTest `aSlowHubLeavesThePageAtItsTop` (hub held on a latch until v4 shows; failed before the fix: row at -121 dp, grid at 64 dp) and `unpinningTheFirstLibraryLeavesTheGridAtItsTop` (14 libraries; failed before the fix). Screenshots unchanged.
- After merging main (#523), LayoutScreenshotTest's workaround (`performScrollToIndex(0)` before each picture) is gone. Its wait for `video-v1` also leaned on the bug: on the tabletop's half page the videos are only composed when the grid jumps down, so it now waits for the page's top (`row-continue`), the idling resource holding it until every answer is in. References unchanged; 3/3 runs green.
- Merged at 5c8dda56.

## What we learned
- Lazy grids anchor on the first visible item's key. Items inserted above it scroll the grid, so content arriving in a different order opens the page part way down.
- A screenshot test that fails on scroll position may be showing a real bug; a test workaround can hide it.
