---
schema: 1
kind: pr
pr: 550
title: Apple Series tab test lost its tap on iPad
date: 2026-11-02
session: b743b7d4-a714-4de3-9952-e11ad221c8e6
source: claude-trail.md
---

## What happened
- Merge aa376a47. main's run 41488772111 (iPad 3/4) failed `PlayerLibrariesUITests.testSeriesTabListsEverySeries`.
- From the xcresult: the test waited only for `libraries.tabs`, which shows while the cards still load. On iPad the cards' answer then picks the first library and its page draws beside the list. The Series tap landed while that page drew: main thread busy, touch-up 185 ms late, the segmented control fell back on Libraries, no GET /api/series.
- Fix (test only): wait for the cards, `screen.library` beside them (iPad) and `settledPage()`, tap, then wait for GET /api/series. App unchanged. Did not reproduce locally (12/12 on a throwaway iPad Pro 11 M5, iOS 26.4).

## What we learned
- A tap that lands while the main thread is busy drawing can be lost; wait for the screen to settle before tapping.
